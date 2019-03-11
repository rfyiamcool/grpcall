package grpcall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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
	// OnReceiveHeaders is called when response headers and message have been received.
	OnReceiveData(metadata.MD, string, error)

	// OnReceiveTrailers is called when response trailers and final RPC status have been received.
	OnReceiveTrailers(*status.Status, metadata.MD)
}

var defaultInEventHooker = new(InEventHooker)

type InEventHooker struct {
}

func (h *InEventHooker) OnReceiveData(md metadata.MD, resp string, respErr error) {
}

func (h *InEventHooker) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
}

type ResultModel struct {
	ResultChan chan string
	SendChan   chan []byte
	DoneChan   chan error
	Data       string
	RespHeader metadata.MD
	IsStream   bool
	Cancel     context.CancelFunc
}

func (r *ResultModel) Read() {
}

func (r *ResultModel) Write() {
}

func (r *ResultModel) IsError() {
}

func (r *ResultModel) IsClose() {
}

func (r *ResultModel) Close() {
}

// RequestSupplier is a function that is called to populate messages for a gRPC operation.
type RequestSupplier func(proto.Message) error

type InvokeHandler struct {
	inEventHandler *EventHandler          // inside
	eventHandler   InvocationEventHandler // custom
}

func newInvokeHandler(event *EventHandler, inEvent InvocationEventHandler) *InvokeHandler {
	return &InvokeHandler{
		eventHandler:   inEvent,
		inEventHandler: event,
	}
}

// InvokeRPC uses the given gRPC channel to invoke the given method.
func (in *InvokeHandler) InvokeRPC(ctx context.Context, source DescriptorSource, ch grpcdynamic.Channel, svc, mth string,
	headers []string, requestData RequestSupplier) (*ResultModel, error) {

	md := MetadataFromHeaders(headers)
	if svc == "" || mth == "" {
		return nil, fmt.Errorf("given method name %s/%s is not in expected format: 'service/method' or 'service.method'", svc, mth)
	}

	dsc, err := source.FindSymbol(svc)
	if err != nil {
		if isNotFoundError(err) {
			return nil, fmt.Errorf("target server not expose service %q in FindSymbol", svc)
		}

		return nil, fmt.Errorf("failed to query for service descriptor %q: %v", svc, err)
	}

	sd, ok := dsc.(*desc.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("target server not expose service %q", svc)
	}

	mtd := sd.FindMethodByName(mth)
	if mtd == nil {
		return nil, fmt.Errorf("service %q does not include a method named %q", svc, mth)
	}

	// we also download any applicable extensions so we can provide full support for parsing user-provided data
	var ext dynamic.ExtensionRegistry
	alreadyFetched := map[string]bool{}
	if err = fetchAllExtensions(source, &ext, mtd.GetInputType(), alreadyFetched); err != nil {
		return nil, fmt.Errorf("error resolving server extensions for message %s: %v", mtd.GetInputType().GetFullyQualifiedName(), err)
	}

	if err = fetchAllExtensions(source, &ext, mtd.GetOutputType(), alreadyFetched); err != nil {
		return nil, fmt.Errorf("error resolving server extensions for message %s: %v", mtd.GetOutputType().GetFullyQualifiedName(), err)
	}

	ctx = metadata.NewOutgoingContext(ctx, md)
	msgFactory := dynamic.NewMessageFactoryWithExtensionRegistry(&ext)
	req := msgFactory.NewMessage(mtd.GetInputType())
	stub := grpcdynamic.NewStubWithMessageFactory(ch, msgFactory)

	if mtd.IsClientStreaming() && mtd.IsServerStreaming() {
		data2PBParser := func(data string) (proto.Message, error) {
			var (
				inData io.Reader
			)

			inData = strings.NewReader(data)
			rf, err := RequestParserFor(source, inData)
			if err != nil {
				return nil, errors.New("request parse and format failed")
			}

			req := msgFactory.NewMessage(mtd.GetInputType())
			rf.Next(req)
			return req, err
		}

		return in.invokeAllStrem(ctx, stub, mtd, in.eventHandler, requestData, req, data2PBParser)

	} else if mtd.IsServerStreaming() {
		data2PBParser := func(data string) (proto.Message, error) {
			var (
				inData io.Reader
			)

			inData = strings.NewReader(data)
			rf, err := RequestParserFor(source, inData)
			if err != nil {
				return nil, errors.New("request parse and format failed")
			}

			req := msgFactory.NewMessage(mtd.GetInputType())
			rf.Next(req)
			return req, err
		}

		return in.invokeServerStream(ctx, stub, mtd, in.eventHandler, requestData, req, data2PBParser)

	} else {
		return in.invokeUnary(ctx, stub, mtd, in.eventHandler, requestData, req)
	}
}

// } else if mtd.IsClientStreaming() {
// 	return invokeClientStream(ctx, stub, mtd, in.eventHandler, requestData, req)

func (in *InvokeHandler) invokeUnary(ctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
	requestData RequestSupplier, req proto.Message) (*ResultModel, error) {

	err := requestData(req)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("error getting request data: %v", err)
	}

	if err != io.EOF {
		// verify there is no second message, which is a usage error
		err := requestData(req)
		if err == nil {
			return nil, fmt.Errorf("method %q is a unary RPC, but request data contained more than 1 message", md.GetFullyQualifiedName())
		} else if err != io.EOF {
			return nil, fmt.Errorf("error getting request data: %v", err)
		}
	}

	var (
		respHeaders  metadata.MD
		respTrailers metadata.MD
	)

	resp, err := stub.InvokeRpc(ctx, md, req, grpc.Trailer(&respTrailers), grpc.Header(&respHeaders))
	stat, ok := status.FromError(err)
	if !ok {
		return nil, fmt.Errorf("grpc call for %q failed: %v", md.GetFullyQualifiedName(), err)
	}

	if stat.Code() == codes.OK {
	}

	respText := in.inEventHandler.FormatResponse(resp)
	result := &ResultModel{
		IsStream:   false,
		Data:       respText,
		RespHeader: respHeaders,
	}
	return result, nil
}

// invokeAllStrem server and client are stream mode
func (in *InvokeHandler) invokeAllStrem(pctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
	requestData RequestSupplier, req proto.Message, dataParser func(data string) (proto.Message, error)) (*ResultModel, error) {

	// for inside logic
	ctx, cancel := context.WithCancel(pctx)

	// invoke rpc with stream mode
	streamReq, err := stub.InvokeRpcBidiStream(ctx, md)
	if err != nil {
		return nil, err
	}

	var (
		resultChan = make(chan string, 10)
		sendChan   = make(chan []byte, 10)
		doneChan   = make(chan error, 1)

		doneNotify = func(err error) {
			select {
			case doneChan <- err:
			default:
				return
			}
		}
	)

	// first send
	err = requestData(req)
	if err != nil {
		return nil, err
	}

	err = streamReq.SendMsg(req)
	if err != nil {
		return nil, err
	}

	// Concurrently upload each request message in the stream
	go func() {
		defer func() {
			cancel()
			streamReq.CloseSend()
		}()

		for {
			select {
			case data, ok := <-sendChan:
				if !ok {
					doneNotify(nil)
					return
				}

				if err := ctx.Err(); err != nil {
					return
				}

				if err := pctx.Err(); err != nil {
					return
				}

				req, err := dataParser(string(data))
				if err != nil {
					doneNotify(err)
					return
				}

				err = streamReq.SendMsg(req)
				if err != nil {
					doneNotify(err)
					return
				}

			case <-pctx.Done():
				doneNotify(nil)
				return

			case <-ctx.Done():
				doneNotify(nil)
				return
			}
		}
	}()

	go func() {
		defer func() {
			cancel()
		}()

		var err error
		var resp proto.Message

		for {
			resp, err = streamReq.RecvMsg()
			if err == io.EOF {
				doneNotify(nil)
				return
			}

			if err != nil {
				doneNotify(err)
				return
			}

			respHeaders, err := streamReq.Header()
			if err != nil {
				doneNotify(err)
				return
			}

			// callback
			respStr := DefaultEventHandler.FormatResponse(resp)
			handler.OnReceiveData(respHeaders, respStr, err)
			resultChan <- respStr

			// zero buffer
			resp.Reset()
		}
	}()

	result := &ResultModel{
		IsStream:   true,
		ResultChan: resultChan,
		SendChan:   sendChan,
		DoneChan:   doneChan,
		Cancel:     cancel,
	}

	return result, nil
}

// invokeServerStream only server is stream mode
func (in *InvokeHandler) invokeServerStream(pctx context.Context, stub grpcdynamic.Stub, md *desc.MethodDescriptor, handler InvocationEventHandler,
	requestData RequestSupplier, req proto.Message,
	dataParser func(data string) (proto.Message, error)) (*ResultModel, error) {

	// for inside logic
	ctx, cancel := context.WithCancel(pctx)

	// init req
	var err error
	err = requestData(req)
	if err != nil {
		return nil, err
	}

	// invoke rpc with stream mode
	streamReq, err := stub.InvokeRpcServerStream(ctx, md, req)
	if err != nil {
		return nil, err
	}

	var (
		resultChan = make(chan string, 10)
		sendChan   = make(chan []byte, 10)
		doneChan   = make(chan error, 1)

		doneNotify = func(err error) {
			select {
			case doneChan <- err:
			default:
				return
			}
		}
	)

	// readLoop
	go func() {
		defer func() {
			cancel()
		}()

		var err error
		var resp proto.Message

		for {
			resp, err = streamReq.RecvMsg()
			if err == io.EOF {
				doneNotify(err)
				return
			}

			if err != nil {
				doneNotify(err)
				return
			}

			respHeaders, err := streamReq.Header()
			if err != nil {
				doneNotify(err)
				return
			}

			// callback
			respStr := DefaultEventHandler.FormatResponse(resp)
			handler.OnReceiveData(respHeaders, respStr, err)
			resultChan <- respStr

			// zero buffer
			resp.Reset()
		}
	}()

	result := &ResultModel{
		IsStream:   true,
		ResultChan: resultChan,
		SendChan:   sendChan,
		DoneChan:   doneChan,
		Cancel:     cancel,
	}

	return result, nil
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
