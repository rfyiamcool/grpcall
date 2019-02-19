package grpcall

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// InvocationEventHandler is a bag of callbacks for handling events that occur in the course
// of invoking an RPC.
type InvocationEventHandler interface {
	// OnResolveMethod is called with a descriptor of the method that is being invoked.
	OnResolveMethod(*desc.MethodDescriptor)

	// OnSendHeaders is called with the request metadata that is being sent.
	OnSendHeaders(metadata.MD)

	// OnReceiveHeaders is called when response headers and message have been received.
	OnReceiveData(metadata.MD, string, error)

	// OnReceiveTrailers is called when response trailers and final RPC status have been received.
	OnReceiveTrailers(*status.Status, metadata.MD)
}

// RequestSupplier is a function that is called to populate messages for a gRPC operation.
type RequestSupplier func(proto.Message) error

type InvokeHandler struct {
	eventHandler *EventHandler
}

func newInvokeHandler(envent *EventHandler) *InvokeHandler {
	return &InvokeHandler{
		eventHandler: envent,
	}
}

// InvokeRPC uses the given gRPC channel to invoke the given method.
func (in *InvokeHandler) InvokeRPC(ctx context.Context, source DescriptorSource, ch grpcdynamic.Channel, svc, mth string,
	headers []string, handler InvocationEventHandler, requestData RequestSupplier) error {

	md := MetadataFromHeaders(headers)
	if svc == "" || mth == "" {
		return fmt.Errorf("given method name %s/%s is not in expected format: 'service/method' or 'service.method'", svc, mth)
	}

	// 获取方法的输入和输出类型
	dsc, err := source.FindSymbol(svc)
	if err != nil {
		if isNotFoundError(err) {
			return fmt.Errorf("target server does not expose service %q", svc)
		}

		return fmt.Errorf("failed to query for service descriptor %q: %v", svc, err)
	}

	// 重试断言
	sd, ok := dsc.(*desc.ServiceDescriptor)
	if !ok {
		return fmt.Errorf("target server does not expose service %q", svc)
	}

	mtd := sd.FindMethodByName(mth)
	if mtd == nil {
		return fmt.Errorf("service %q does not include a method named %q", svc, mth)
	}

	// handler.OnResolveMethod(mtd)

	// we also download any applicable extensions so we can provide full support for parsing user-provided data
	var ext dynamic.ExtensionRegistry
	alreadyFetched := map[string]bool{}
	if err = fetchAllExtensions(source, &ext, mtd.GetInputType(), alreadyFetched); err != nil {
		return fmt.Errorf("error resolving server extensions for message %s: %v", mtd.GetInputType().GetFullyQualifiedName(), err)
	}
	if err = fetchAllExtensions(source, &ext, mtd.GetOutputType(), alreadyFetched); err != nil {
		return fmt.Errorf("error resolving server extensions for message %s: %v", mtd.GetOutputType().GetFullyQualifiedName(), err)
	}

	msgFactory := dynamic.NewMessageFactoryWithExtensionRegistry(&ext)
	req := msgFactory.NewMessage(mtd.GetInputType())

	handler.OnSendHeaders(md)
	ctx = metadata.NewOutgoingContext(ctx, md)

	stub := grpcdynamic.NewStubWithMessageFactory(ch, msgFactory)
	ctx, cancel := context.WithCancel(ctx)

	defer cancel()

	if mtd.IsClientStreaming() && mtd.IsServerStreaming() {
		return in.invokeAllStrem(ctx, stub, mtd, handler, requestData, req)
	} else {
		return in.invokeUnary(ctx, stub, mtd, handler, requestData, req)
	}

	// } else if mtd.IsClientStreaming() {
	// 	return invokeClientStream(ctx, stub, mtd, handler, requestData, req)

	// } else if mtd.IsServerStreaming() {
	// 	return invokeServerStream(ctx, stub, mtd, handler, requestData, req)
}

func (in *InvokeHandler) invokeUnary(ctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
	requestData RequestSupplier, req proto.Message) error {

	err := requestData(req)
	if err != nil && err != io.EOF {
		return fmt.Errorf("error getting request data: %v", err)
	}

	if err != io.EOF {
		// verify there is no second message, which is a usage error
		err := requestData(req)
		if err == nil {
			return fmt.Errorf("method %q is a unary RPC, but request data contained more than 1 message", md.GetFullyQualifiedName())
		} else if err != io.EOF {
			return fmt.Errorf("error getting request data: %v", err)
		}
	}

	var respHeaders metadata.MD
	var respTrailers metadata.MD

	resp, err := stub.InvokeRpc(ctx, md, req, grpc.Trailer(&respTrailers), grpc.Header(&respHeaders))

	stat, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("grpc call for %q failed: %v", md.GetFullyQualifiedName(), err)
	}

	if stat.Code() == codes.OK {
		//
	}

	respText := in.eventHandler.FormatResponse(resp)
	handler.OnReceiveData(respHeaders, respText, err)

	return nil
}

func (in *InvokeHandler) invokeAllStrem(ctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
	requestData RequestSupplier, req proto.Message) error {

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// invoke rpc by stream
	streamReq, err := stub.InvokeRpcBidiStream(ctx, md)
	if err != nil {
		return err
	}

	var (
		wg      sync.WaitGroup
		sendErr atomic.Value
	)

	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Concurrently upload each request message in the stream
		var err error
		err = requestData(req)

		if err == io.EOF {
			err = streamReq.CloseSend()
			return
		}

		if err != nil {
			err = fmt.Errorf("error getting request data: %s", err.Error())
			return
		}

		err = streamReq.SendMsg(req)
		req.Reset() // zero state

		if err != nil {
			sendErr.Store(err)
			cancel()
		}
	}()

	// fetch each response message
	for err == nil {
		var resp proto.Message
		resp, err = streamReq.RecvMsg()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			break
		}

		// 测试是否最新
		respHeaders, err := streamReq.Header()
		if err != nil {
			break
		}

		// handler.OnReceiveResponse(resp)
		respStr := DefaultEventHandler.FormatResponse(resp)
		handler.OnReceiveData(respHeaders, respStr, err)
	}

	if se, ok := sendErr.Load().(error); ok && se != io.EOF {
		err = se
	}

	stat, ok := status.FromError(err)
	if !ok {
		// Error codes sent from the server will get printed differently below.
		// So just bail for other kinds of errors here.
		return fmt.Errorf("grpc call for %q failed: %v", md.GetFullyQualifiedName(), err)
	}

	handler.OnReceiveTrailers(stat, streamReq.Trailer())
	return nil
}

// func (in *InvokeHandler) invokeClientStream(ctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
// 	requestData RequestSupplier, req proto.Message) error {

// 	// invoke the RPC!
// 	str, err := stub.InvokeRpcClientStream(ctx, md)

// 	// Upload each request message in the stream
// 	var resp proto.Message
// 	for err == nil {
// 		err = requestData(req)
// 		if err == io.EOF {
// 			resp, err = str.CloseAndReceive()
// 			break
// 		}
// 		if err != nil {
// 			return fmt.Errorf("error getting request data: %v", err)
// 		}

// 		err = str.SendMsg(req)
// 		if err == io.EOF {
// 			// We get EOF on send if the server says "go away"
// 			// We have to use CloseAndReceive to get the actual code
// 			resp, err = str.CloseAndReceive()
// 			break
// 		}

// 		req.Reset()
// 	}

// 	// finally, process response data
// 	stat, ok := status.FromError(err)
// 	if !ok {
// 		// Error codes sent from the server will get printed differently below.
// 		// So just bail for other kinds of errors here.
// 		return fmt.Errorf("grpc call for %q failed: %v", md.GetFullyQualifiedName(), err)
// 	}

// 	if respHeaders, err := str.Header(); err == nil {
// 		handler.OnReceiveHeaders(respHeaders)
// 	}

// 	if stat.Code() == codes.OK {
// 		handler.OnReceiveResponse(resp)
// 	}

// 	// handler.OnReceiveTrailers(stat, str.Trailer())

// 	return nil
// }

// func (in *InvokeHandler) invokeServerStream(ctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
// 	requestData RequestSupplier, req proto.Message) error {

// 	err := requestData(req)
// 	if err != nil && err != io.EOF {
// 		return fmt.Errorf("error getting request data: %v", err)
// 	}
// 	if err != io.EOF {
// 		// verify there is no second message, which is a usage error
// 		err := requestData(req)
// 		if err == nil {
// 			return fmt.Errorf("method %q is a server-streaming RPC, but request data contained more than 1 message", md.GetFullyQualifiedName())
// 		} else if err != io.EOF {
// 			return fmt.Errorf("error getting request data: %v", err)
// 		}
// 	}

// 	// Now we can actually invoke the RPC!
// 	str, err := stub.InvokeRpcServerStream(ctx, md, req)

// 	if respHeaders, err := str.Header(); err == nil {
// 		handler.OnReceiveHeaders(respHeaders)
// 	}

// 	// Download each response message
// 	for err == nil {
// 		var resp proto.Message
// 		resp, err = str.RecvMsg()
// 		if err != nil {
// 			if err == io.EOF {
// 				err = nil
// 			}
// 			break
// 		}
// 		handler.OnReceiveResponse(resp)
// 	}

// 	stat, ok := status.FromError(err)
// 	if !ok {
// 		// Error codes sent from the server will get printed differently below.
// 		// So just bail for other kinds of errors here.
// 		return fmt.Errorf("grpc call for %q failed: %v", md.GetFullyQualifiedName(), err)
// 	}

// 	handler.OnReceiveTrailers(stat, str.Trailer())

// 	return nil
// }

type notFoundError string

func notFound(kind, name string) error {
	return notFoundError(fmt.Sprintf("%s not found: %s", kind, name))
}

func (e notFoundError) Error() string {
	return string(e)
}

func isNotFoundError(err error) bool {
	if grpcreflect.IsElementNotFoundError(err) {
		return true
	}

	_, ok := err.(notFoundError)
	return ok
}
