package main

import (
	"fmt"

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func main() {
	fmt.Println("start...")
	defer fmt.Println("end...")

	grpcall.SetProtoSetFiles("helloworld.protoset")
	grpcall.InitDescSource()

	var handler = DefaultEventHandler{}
	var sendBody string

	grpcEnter, err := grpcall.New(&handler)
	grpcEnter.Init()

	sendBody = `{"name": "xiaorui.cc"}`
	err = grpcEnter.Call("127.0.0.1:50051", "helloworld.Greeter", "SayHello", sendBody)
	fmt.Println("return err: ", err)

	sendBody = `{"msg": "xxxxxxxxx"}`
	err = grpcEnter.Call("127.0.0.1:50051", "helloworld.SimpleService", "SimpleRPC", sendBody)
	fmt.Println("return err: ", err)
}

type DefaultEventHandler struct {
}

func (h *DefaultEventHandler) OnReceiveData(md metadata.MD, resp string, respErr error) {
	fmt.Printf("\nResponse headers received:\n%v\n", md)
	fmt.Printf("\nResponse contents: \n %v\n", resp)
	fmt.Println(respErr)
}

func (h *DefaultEventHandler) OnStreamSend(data string) {
}

func (h *DefaultEventHandler) OnResolveMethod(md *desc.MethodDescriptor) {
}

func (h *DefaultEventHandler) OnSendHeaders(md metadata.MD) {
}

func (h *DefaultEventHandler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
}
