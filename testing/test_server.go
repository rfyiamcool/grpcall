package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "git.biss.com/golib/grpcall/testing/helloworld"
)

const (
	port = ":50051"
)

// server is used to implement helloworld.GreeterServer.
type server struct{}

// SayHello implements helloworld.GreeterServer
func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	log.Println("get client request name :" + in.Name)
	return &pb.HelloReply{Message: "hehe main: Hello-" + in.Name}, nil
}

type simpleServer struct{}

func (s *simpleServer) SimpleRPC(stream pb.SimpleService_SimpleRPCServer) error {
	log.Println("Started stream first")

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()

		for {
			in, err := stream.Recv()
			log.Println("recv data: ", in, "err: ", err)
			if err != nil {
				return
			}
		}
	}()

	go func() error {
		defer wg.Done()

		var err error
		for {
			d := pb.SimpleData{Msg: "haha"}
			err = stream.Send(&d)
			if err == io.EOF {
				log.Println(err)
				return nil
			}

			if err != nil {
				log.Println(err)
				return err
			}

			log.Println("send body ok")
			time.Sleep(2 * time.Second)
		}
	}()

	wg.Wait()
	log.Println("client exited")
	return nil
}

func main() {
	go func() {
		http.ListenAndServe("0.0.0.0:8081", nil)
	}()

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
		return
	}

	s := grpc.NewServer()
	srv := &server{}
	srvm := &simpleServer{}

	pb.RegisterGreeterServer(s, srv)
	pb.RegisterSimpleServiceServer(s, srvm)
	reflection.Register(s)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
