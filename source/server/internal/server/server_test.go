package server

import (
	"context"
	"log"
	"net"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/pkg/proto" // Import the generated protobuf package

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

var lis *bufconn.Listener

// Mocks for testing
type mockProvider struct {
	name string
}

func (m *mockProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	return &agent.Response{Output: "Processed by " + m.name}, nil
}

func (m *mockProvider) Name() string {
	return m.name
}

type mockRouter struct{}

func (m *mockRouter) SelectProvider(req *agent.Request) (agent.ModelProvider, error) {
	if req.ProviderConfig != nil {
		return &mockProvider{name: req.ProviderConfig.Provider}, nil
	}
	return &mockProvider{name: "MockLocal"}, nil
}

func (m *mockRouter) ClassifyIntent(req *agent.Request) (agent.Intent, error) {
	return agent.IntentChat, nil
}

func (m *mockRouter) GetModelProviders() map[string]agent.ModelProvider {
	return map[string]agent.ModelProvider{
		"LocalModel": &mockProvider{name: "MockLocal"},
		"CloudModel": &mockProvider{name: "MockCloud"},
	}
}

type mockCoordinator struct{}

func (m *mockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string) (*agent.Response, error) {
	return &agent.Response{Output: "coordinated output"}, nil
}

func init() {
	lis = bufconn.Listen(bufSize)
	s := grpc.NewServer()
	// NewGenerationCoordinator signature changed
	coordinator := &mockCoordinator{}
	orchestrator := agent.NewAgent(&mockRouter{}, coordinator)
	proto.RegisterAgentServer(s, NewServer(orchestrator))
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Server exited with error: %v", err)
		}
	}()
}

func bufDialer(context.Context, string) (net.Conn, error) {
	return lis.Dial()
}

func TestAgentServer_ProcessRequest(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	client := proto.NewAgentClient(conn)

	// Test case 1: Basic request
	req := &proto.ProcessRequestRequest{Input: "Hello AI"}
	res, err := client.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if res.Output == "" {
		t.Errorf("Expected output, got empty string")
	}

	// Add more test cases here as functionality expands
}