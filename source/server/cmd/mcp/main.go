package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	mcpserver "cercano/source/server/internal/mcp"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cercano/source/server/pkg/proto"
)

func main() {
	grpcAddr := flag.String("grpc-addr", "localhost:50052", "Address of the Cercano gRPC server")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "Cercano MCP server starting (gRPC backend: %s)...\n", *grpcAddr)

	// Connect to the Cercano gRPC server.
	conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gRPC server at %s: %v\n", *grpcAddr, err)
		os.Exit(1)
	}
	defer conn.Close()

	grpcClient := proto.NewAgentClient(conn)

	// Create the MCP server backed by the gRPC client.
	s := mcpserver.NewServer(grpcClient)

	// Serve on stdio.
	if err := s.MCPServer().Run(context.Background(), &gomcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
