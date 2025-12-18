package main

import (
	"fmt"
	"log"
	"net"

	"cercano/source/internal/agent"
	"cercano/source/proto"

	"google.golang.org/grpc"
)

func main() {
	fmt.Println("Starting Cercano AI Agent gRPC server...")

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	proto.RegisterAgentServer(s, agent.NewServer())

	fmt.Printf("Server listening at %v\n", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
