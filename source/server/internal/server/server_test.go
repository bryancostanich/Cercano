package server

import (
	"context"
	"log"
	"net"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
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

func (m *mockRouter) SelectProvider(req *agent.Request, intent agent.Intent) (agent.ModelProvider, error) {
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

func (m *mockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress agent.ProgressFunc) (*agent.Response, error) {
	return &agent.Response{Output: "coordinated output"}, nil
}

func init() {
	lis = bufconn.Listen(bufSize)
	s := grpc.NewServer()
	coordinator := &mockCoordinator{}
	orchestrator := agent.NewAgent(&mockRouter{}, coordinator)
	proto.RegisterAgentServer(s, NewServer(orchestrator, nil, nil, nil, nil))
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

func TestUpdateConfig_OllamaURL(t *testing.T) {
	provider := llm.NewOllamaProvider("test-model", "http://localhost:11434")
	srv := NewServer(nil, provider, nil, nil, nil)

	// Set a valid remote URL
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OllamaUrl: "http://mac-studio.local:11434",
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if !resp.Success {
		t.Errorf("Expected success, got: %s", resp.Message)
	}

	// Verify the provider's BaseURL was updated
	if provider.GetBaseURL() != "http://mac-studio.local:11434" {
		t.Errorf("Expected BaseURL 'http://mac-studio.local:11434', got '%s'", provider.GetBaseURL())
	}
}

func TestUpdateConfig_OllamaURL_InvalidURL(t *testing.T) {
	provider := llm.NewOllamaProvider("test-model", "http://localhost:11434")
	srv := NewServer(nil, provider, nil, nil, nil)

	// Set an invalid URL — should fail validation
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OllamaUrl: "not-a-valid-url",
	})
	if err != nil {
		t.Fatalf("UpdateConfig returned error: %v", err)
	}
	if resp.Success {
		t.Error("Expected failure for invalid URL, got success")
	}

	// BaseURL should remain unchanged
	if provider.GetBaseURL() != "http://localhost:11434" {
		t.Errorf("Expected BaseURL unchanged, got '%s'", provider.GetBaseURL())
	}
}

func TestUpdateConfig_OllamaURL_WithModel(t *testing.T) {
	provider := llm.NewOllamaProvider("test-model", "http://localhost:11434")
	srv := NewServer(nil, provider, nil, nil, nil)

	// Set both URL and model in one call
	resp, err := srv.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
		OllamaUrl:  "http://192.168.1.100:11434",
		LocalModel: "llama3",
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if !resp.Success {
		t.Errorf("Expected success, got: %s", resp.Message)
	}

	if provider.GetBaseURL() != "http://192.168.1.100:11434" {
		t.Errorf("Expected BaseURL 'http://192.168.1.100:11434', got '%s'", provider.GetBaseURL())
	}
	if provider.Name() != "llama3" {
		t.Errorf("Expected model 'llama3', got '%s'", provider.Name())
	}
}