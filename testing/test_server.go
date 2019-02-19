package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/grpcall/testing/helloworld"
)

const (
	port = ":50051"
)

// server is used to implement helloworld.GreeterServer.
type server struct{}

// SayHello implements helloworld.GreeterServer
func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	fmt.Println("######### get client request name :" + in.Name)
	return &pb.HelloReply{Message: "hehe main: Hello-" + in.Name}, nil
}

type simpleServer struct {
}

func (s *simpleServer) SimpleRPC(stream pb.SimpleService_SimpleRPCServer) error {
	log.Println("Started stream")
	in, err := stream.Recv()
	log.Println("Received value")
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	log.Println("Got " + in.Msg)

	for {
		d := pb.SimpleData{Msg: "haha"}
		stream.Send(&d)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		time.Sleep(1 * time.Second)
	}
}

func main() {
	go func() {
		http.ListenAndServe("0.0.0.0:8081", nil)
	}()

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer(
		grpc.MaxConcurrentStreams(64),
		grpc.WriteBufferSize(64*1024),
	)

	srv := &server{}
	srvm := &simpleServer{}
	pb.RegisterGreeterServer(s, srv)
	pb.RegisterSimpleServiceServer(s, srvm)
	// Register reflection service on gRPC server.
	reflection.Register(s)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
