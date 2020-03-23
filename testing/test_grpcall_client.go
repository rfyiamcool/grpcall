package main

import (
	"fmt"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"grpcall"
)

func main() {
	fmt.Println("start...")
	defer fmt.Println("end...")

	grpcall.SetProtoSetFiles("helloworld.protoset")
	err := grpcall.InitDescSource()
	if err != nil {
		panic(err.Error())
	}

	var handler = DefaultEventHandler{}
	var sendBody string

	grpcEnter, err := grpcall.New(
		grpcall.SetHookHandler(&handler),
	)
	grpcEnter.Init()

	sendBody = `{"name": "golang world"}`
	res, err := grpcEnter.Call("127.0.0.1:50051", "helloworld.Greeter", "SayHello", sendBody)
	fmt.Printf("%+v \n", res)
	fmt.Println("req/reply return err: ", err)

}

type DefaultEventHandler struct {
	sendChan chan []byte
}

func (h *DefaultEventHandler) OnReceiveData(md metadata.MD, resp string, respErr error) {
}

func (h *DefaultEventHandler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
}
