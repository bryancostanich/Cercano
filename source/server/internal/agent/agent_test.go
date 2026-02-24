package agent

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/session"
)

type mockRouter struct {
	intent         Intent
	provider       ModelProvider
	ModelProviders map[string]ModelProvider
}

func (m *mockRouter) SelectProvider(req *Request, intent Intent) (ModelProvider, error) {
	return m.provider, nil
}

func (m *mockRouter) ClassifyIntent(req *Request) (Intent, error) {
	return m.intent, nil
}

func (m *mockRouter) GetModelProviders() map[string]ModelProvider {
	return m.ModelProviders
}

type mockModelProvider struct{ name string }

func (m *mockModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: "provider output"}, nil
}
func (m *mockModelProvider) Name() string { return m.name }

// mockModelProviderFunc captures the input it receives for assertions.
type mockModelProviderFunc struct {
	name        string
	capturedReq *Request
	response    *Response
}

func (m *mockModelProviderFunc) Process(ctx context.Context, req *Request) (*Response, error) {
	m.capturedReq = req
	return m.response, nil
}
func (m *mockModelProviderFunc) Name() string { return m.name }

type mockCoordinator struct{}

func (m *mockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
	return &Response{Output: "coordinated output"}, nil
}

func TestAgent_ProcessRequest_ChatIntent(t *testing.T) {
	router := &mockRouter{intent: IntentChat, provider: &mockModelProvider{name: "mock"}}
	// Mock coordinator needs the new signature if initialized via NewGenerationCoordinator, 
	// but here it's a mock struct implementing the interface.
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	res, err := a.ProcessRequest(ctx, &Request{Input: "hello"})

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if res.Output != "provider output" {
		t.Errorf("Expected provider output, got %s", res.Output)
	}
}

func TestAgent_ProcessRequest_CodingIntent(t *testing.T) {
	router := &mockRouter{intent: IntentCoding, provider: &mockModelProvider{name: "mock"}}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	req := &Request{
		Input:    "write code",
		WorkDir:  "/tmp",
		FileName: "test.go",
	}
	res, err := a.ProcessRequest(ctx, req)

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if res.Output != "coordinated output" {
		t.Errorf("Expected coordinated output, got %s", res.Output)
	}
}

func TestAgent_ProcessRequest_UnitTestFilenameAdjustment(t *testing.T) {
	router := &mockRouter{intent: IntentCoding, provider: &mockModelProvider{name: "mock"}}
	
	capturedFile := ""
	coordinator := &MockCoordinator{
		CoordinateFunc: func(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
			capturedFile = fileName
			return &Response{Output: "ok"}, nil
		},
	}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	req := &Request{
		Input:    "Generate unit tests for this",
		WorkDir:  "/tmp",
		FileName: "logic.go",
	}
	_, err := a.ProcessRequest(ctx, req)

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if capturedFile != "logic_test.go" {
		t.Errorf("Expected logic_test.go for singular, got %s", capturedFile)
	}

	// Test plural
	req.Input = "Generate unit tests"
	_, err = a.ProcessRequest(ctx, req)
	if capturedFile != "logic_test.go" {
		t.Errorf("Expected logic_test.go for plural, got %s", capturedFile)
	}
}

type MockCoordinator struct {
	CoordinateFunc func(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error)
}

func (m *MockCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
	return m.CoordinateFunc(ctx, instruction, inputCode, workDir, fileName, progress)
}

// MockStreamableCoordinator implements both Coordinator and StreamableCoordinator.
type MockStreamableCoordinator struct {
	CoordinateFunc       func(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error)
	CoordinateStreamFunc func(ctx context.Context, instruction, inputCode, workDir, fileName string) (iter.Seq2[*session.Event, error], func() (*Response, error), error)
}

func (m *MockStreamableCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
	return m.CoordinateFunc(ctx, instruction, inputCode, workDir, fileName, progress)
}

func (m *MockStreamableCoordinator) CoordinateStream(ctx context.Context, instruction, inputCode, workDir, fileName string) (iter.Seq2[*session.Event, error], func() (*Response, error), error) {
	return m.CoordinateStreamFunc(ctx, instruction, inputCode, workDir, fileName)
}

func TestAgent_ProcessRequestStream_UsesStreamableCoordinator(t *testing.T) {
	router := &mockRouter{intent: IntentCoding, provider: &mockModelProvider{name: "mock"}}

	streamCalled := false
	coord := &MockStreamableCoordinator{
		CoordinateFunc: func(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error) {
			t.Error("Coordinate should not be called when StreamableCoordinator is available")
			return &Response{Output: "non-stream"}, nil
		},
		CoordinateStreamFunc: func(ctx context.Context, instruction, inputCode, workDir, fileName string) (iter.Seq2[*session.Event, error], func() (*Response, error), error) {
			streamCalled = true
			events := func(yield func(*session.Event, error) bool) {
				// Yield one generator event
				ev := session.NewEvent("inv-1")
				ev.Author = "generator"
				yield(ev, nil)
			}
			finalize := func() (*Response, error) {
				return &Response{
					Output:      "streamed output",
					FileChanges: []FileChange{{Path: "test.go", Content: "code", Action: "UPDATE"}},
				}, nil
			}
			return events, finalize, nil
		},
	}

	a := NewAgent(router, coord)

	var progressMessages []string
	progress := func(msg string) { progressMessages = append(progressMessages, msg) }

	res, err := a.ProcessRequestStream(context.Background(), &Request{
		Input:    "write code",
		WorkDir:  "/tmp",
		FileName: "test.go",
	}, progress, nil)

	if err != nil {
		t.Fatalf("ProcessRequestStream failed: %v", err)
	}
	if !streamCalled {
		t.Error("expected CoordinateStream to be called")
	}
	if res.Output != "streamed output" {
		t.Errorf("expected 'streamed output', got %q", res.Output)
	}
	// Should have received progress from MapEventToProgress for generator event
	found := false
	for _, msg := range progressMessages {
		if msg == "Generating code..." {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Generating code...' in progress messages, got: %v", progressMessages)
	}
}

func TestAgent_ProcessRequestStream_FallsBackToCoordinate(t *testing.T) {
	router := &mockRouter{intent: IntentCoding, provider: &mockModelProvider{name: "mock"}}

	// Use a plain mockCoordinator (not StreamableCoordinator)
	coord := &mockCoordinator{}
	a := NewAgent(router, coord)

	res, err := a.ProcessRequestStream(context.Background(), &Request{
		Input:    "write code",
		WorkDir:  "/tmp",
		FileName: "test.go",
	}, nil, nil)

	if err != nil {
		t.Fatalf("ProcessRequestStream failed: %v", err)
	}
	if res.Output != "coordinated output" {
		t.Errorf("expected 'coordinated output', got %q", res.Output)
	}
}

// mockStreamingModelProvider implements StreamingModelProvider for agent tests.
type mockStreamingModelProvider struct {
	name   string
	tokens []string
}

func (m *mockStreamingModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: strings.Join(m.tokens, "")}, nil
}
func (m *mockStreamingModelProvider) Name() string { return m.name }
func (m *mockStreamingModelProvider) ProcessStream(ctx context.Context, req *Request, onToken TokenFunc) (*Response, error) {
	var out strings.Builder
	for _, tok := range m.tokens {
		out.WriteString(tok)
		if onToken != nil {
			onToken(tok)
		}
	}
	return &Response{Output: out.String()}, nil
}

func TestAgent_ProcessRequestStream_ChatWithTokenStreaming(t *testing.T) {
	provider := &mockStreamingModelProvider{
		name:   "streaming-mock",
		tokens: []string{"Hello", " ", "world"},
	}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	var receivedTokens []string
	tokenCb := func(token string) { receivedTokens = append(receivedTokens, token) }

	res, err := a.ProcessRequestStream(context.Background(), &Request{Input: "hi"}, nil, tokenCb)
	if err != nil {
		t.Fatalf("ProcessRequestStream failed: %v", err)
	}
	if res.Output != "Hello world" {
		t.Errorf("expected %q, got %q", "Hello world", res.Output)
	}
	if len(receivedTokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(receivedTokens))
	}
	for i, expected := range []string{"Hello", " ", "world"} {
		if receivedTokens[i] != expected {
			t.Errorf("token %d: expected %q, got %q", i, expected, receivedTokens[i])
		}
	}
}

func TestAgent_ProcessRequestStream_ChatFallbackToNonStreaming(t *testing.T) {
	// Non-streaming provider — should still work via Process()
	provider := &mockModelProvider{name: "non-streaming"}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	tokenCalled := false
	res, err := a.ProcessRequestStream(context.Background(), &Request{Input: "hi"}, nil, func(token string) {
		tokenCalled = true
	})
	if err != nil {
		t.Fatalf("ProcessRequestStream failed: %v", err)
	}
	if res.Output != "provider output" {
		t.Errorf("expected %q, got %q", "provider output", res.Output)
	}
	if tokenCalled {
		t.Error("tokenProgress should not be called for non-streaming provider")
	}
}

func TestAgent_ProcessRequestStream_NilTokenCallback(t *testing.T) {
	// StreamingModelProvider with nil tokenProgress should use blocking path
	provider := &mockStreamingModelProvider{
		name:   "streaming-mock",
		tokens: []string{"Hello"},
	}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	// nil tokenProgress — should fall back to Process()
	res, err := a.ProcessRequestStream(context.Background(), &Request{Input: "hi"}, nil, nil)
	if err != nil {
		t.Fatalf("ProcessRequestStream failed: %v", err)
	}
	if res.Output != "Hello" {
		t.Errorf("expected %q, got %q", "Hello", res.Output)
	}
}

func TestAgent_ProcessRequest_ExplicitCloudOverride(t *testing.T) {
	// Router says LocalModel, but input says "use cloud"
	cloudProvider := &mockModelProvider{name: "CloudModel"}
	router := &mockRouter{
		intent:   IntentChat,
		provider: &mockModelProvider{name: "LocalModel"},
		ModelProviders: map[string]ModelProvider{
			"CloudModel": cloudProvider,
		},
	}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator)

	ctx := context.Background()
	// Input contains "use cloud"
	res, err := a.ProcessRequest(ctx, &Request{Input: "use cloud to explain black holes"})

	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	// We expect it to be processed by CloudModel even though router said Local
	if res.RoutingMetadata.ModelName != "CloudModel" {
		t.Errorf("Expected CloudModel override, got %s", res.RoutingMetadata.ModelName)
	}
}

func TestAgent_ProcessRequest_InjectsHistory(t *testing.T) {
	// Pre-populate history, verify provider receives augmented input containing history.
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 3)
	ctx := context.Background()

	// Seed a prior turn
	cs.AppendTurn(ctx, "test-conv-1", "tell me about this calculator class",
		"This calculator provides Add, Subtract. You could add Power, Modulo, SquareRoot...")

	provider := &mockModelProviderFunc{
		name:     "mock",
		response: &Response{Output: "ok"},
	}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator, WithConversationStore(cs))

	_, err := a.ProcessRequest(ctx, &Request{
		Input:          "add those to the file",
		ConversationID: "test-conv-1",
	})
	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	// The provider should have received the augmented input with history
	if provider.capturedReq == nil {
		t.Fatal("expected provider to capture request")
	}
	captured := provider.capturedReq.Input
	if !strings.Contains(captured, "tell me about this calculator class") {
		t.Errorf("expected history in augmented input, got %q", captured)
	}
	if !strings.Contains(captured, "add those to the file") {
		t.Errorf("expected original input in augmented input, got %q", captured)
	}
}

func TestAgent_ProcessRequest_StoresTurnAfterResponse(t *testing.T) {
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 3)
	ctx := context.Background()

	provider := &mockModelProviderFunc{
		name:     "mock",
		response: &Response{Output: "here is your answer"},
	}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator, WithConversationStore(cs))

	_, err := a.ProcessRequest(ctx, &Request{
		Input:          "what is 2+2?",
		ConversationID: "test-conv-2",
	})
	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	// Verify the turn was stored
	history, err := cs.LoadHistory(ctx, "test-conv-2")
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}
	if !strings.Contains(history, "what is 2+2?") {
		t.Errorf("expected user message in stored history, got %q", history)
	}
	if !strings.Contains(history, "here is your answer") {
		t.Errorf("expected assistant response in stored history, got %q", history)
	}
}

func TestAgent_ProcessRequest_NoConversationID_NoHistory(t *testing.T) {
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 3)

	provider := &mockModelProviderFunc{
		name:     "mock",
		response: &Response{Output: "ok"},
	}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator, WithConversationStore(cs))

	ctx := context.Background()
	_, err := a.ProcessRequest(ctx, &Request{
		Input: "hello",
		// No ConversationID
	})
	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	// Should receive original input unchanged (no history prepended)
	if provider.capturedReq.Input != "hello" {
		t.Errorf("expected original input 'hello', got %q", provider.capturedReq.Input)
	}
}

func TestAgent_ProcessRequest_NilConversationStore(t *testing.T) {
	// Backward compat: Agent without conversation store works fine
	provider := &mockModelProviderFunc{
		name:     "mock",
		response: &Response{Output: "ok"},
	}
	router := &mockRouter{intent: IntentChat, provider: provider}
	coordinator := &mockCoordinator{}
	a := NewAgent(router, coordinator) // No WithConversationStore

	ctx := context.Background()
	_, err := a.ProcessRequest(ctx, &Request{
		Input:          "hello",
		ConversationID: "some-id",
	})
	if err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}
	if provider.capturedReq.Input != "hello" {
		t.Errorf("expected original input, got %q", provider.capturedReq.Input)
	}
}
