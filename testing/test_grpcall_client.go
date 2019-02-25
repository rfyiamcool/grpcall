package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/rfyiamcool/golib/grpcall"
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

	grpcEnter, err := grpcall.New(
		grpcall.SetHookHandler(&handler),
	)
	grpcEnter.Init()

	sendBody = `{"name": "xiaorui.cc"}`
	res, err := grpcEnter.Call("127.0.0.1:50051", "helloworld.Greeter", "SayHello", sendBody)
	fmt.Printf("%+v \n", res)
	fmt.Println("req/reply return err: ", err)

	sendBody = `{"msg": "hehe world"}`
	res, err = grpcEnter.Call("127.0.0.1:50051", "helloworld.SimpleService", "SimpleRPC", sendBody)
	fmt.Printf("%+v \n", res)
	fmt.Println("stream return err: ", err)

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		for {
			body := fmt.Sprintf(`{"msg": "hehe world %s"}`, time.Now().String())
			select {
			case res.SendChan <- []byte(body):
				// fmt.Println("send", body)
				time.Sleep(3 * time.Second)

			case <-res.DoneChan:
				return

			default:
				return
			}
		}
	}()

	go func() {
		defer wg.Done()

		for {
			select {
			case msg, ok := <-res.ResultChan:
				fmt.Println("recv data:", msg, ok)
			case err := <-res.DoneChan:
				fmt.Println("done chan: ", err)
				return
			}
		}
	}()

	wg.Wait()
}

type DefaultEventHandler struct {
	sendChan chan []byte
}

func (h *DefaultEventHandler) OnReceiveData(md metadata.MD, resp string, respErr error) {
}

func (h *DefaultEventHandler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
}
