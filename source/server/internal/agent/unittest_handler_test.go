package agent_test

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/router"
)

// SpyProvider captures the request for verification.
type SpyProvider struct {
	CapturedRequest *router.Request
	Response        *router.Response
	Err             error
}

func (s *SpyProvider) Process(ctx context.Context, req *router.Request) (*router.Response, error) {
	s.CapturedRequest = req
	if s.Err != nil {
		return nil, s.Err
	}
	// Return a default response if none set, to avoid nil panics
	if s.Response == nil {
		return &router.Response{Output: "default spy response"}, nil
	}
	return s.Response, nil
}

func (s *SpyProvider) Name() string {
	return "spy-provider"
}

func TestUnitTestHandler_Generate_ConstructsPrompt(t *testing.T) {
	spy := &SpyProvider{}
	handler := agent.NewUnitTestHandler(spy)

	inputCode := "func Add(a, b int) int { return a + b }"
	ctx := context.Background()

	_, err := handler.Generate(ctx, inputCode)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if spy.CapturedRequest == nil {
		t.Fatal("Provider.Process was not called")
	}

	// Verify prompt engineering (the plumbing)
	// We expect the prompt to contain instructions and the input code.
	prompt := spy.CapturedRequest.Input
	if !strings.Contains(prompt, inputCode) {
		t.Errorf("Prompt should contain the input code")
	}
	if !strings.Contains(prompt, "Write table-driven unit tests") { // Expecting this instruction
		t.Errorf("Prompt should contain specific instructions (e.g., 'Write table-driven unit tests')")
	}
}

func TestUnitTestHandler_Generate_HandlesError(t *testing.T) {
	expectedErr := context.DeadlineExceeded
	spy := &SpyProvider{Err: expectedErr}
	handler := agent.NewUnitTestHandler(spy)

	_, err := handler.Generate(context.Background(), "func foo()")
	
	if err == nil {
		t.Fatal("Expected error from handler, got nil")
	}
	// In a real app we might wrap errors, checking contains/Is is good practice
	if !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Errorf("Expected error containing '%v', got '%v'", expectedErr, err)
	}
}

func TestUnitTestHandler_Fix_ConstructsPrompt(t *testing.T) {
	spy := &SpyProvider{}
	handler := agent.NewUnitTestHandler(spy)

	inputCode := "func Add(a, b int) { return a + b }"
	errorMsg := "too many return values"
	ctx := context.Background()

	_, err := handler.Fix(ctx, inputCode, errorMsg)
	if err != nil {
		t.Fatalf("Fix failed: %v", err)
	}

	if spy.CapturedRequest == nil {
		t.Fatal("Provider.Process was not called")
	}

	prompt := spy.CapturedRequest.Input
	if !strings.Contains(prompt, inputCode) {
		t.Errorf("Prompt should contain the input code")
	}
	if !strings.Contains(prompt, errorMsg) {
		t.Errorf("Prompt should contain the error message")
	}
	if !strings.Contains(prompt, "Please fix it") {
		t.Errorf("Prompt should contain 'Please fix it'")
	}
}
