package adapters_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	agentmod "cercano/source/server/internal/agent"
	"cercano/source/server/internal/loop/adapters"
)

// ---- stub ModelProvider ----

type stubProvider struct {
	name      string
	output    string
	err       error
	calls     int
	lastInput string
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Process(_ context.Context, req *agentmod.Request) (*agentmod.Response, error) {
	s.calls++
	s.lastInput = req.Input
	if s.err != nil {
		return nil, s.err
	}
	return &agentmod.Response{Output: s.output}, nil
}

// ---- stub Validator ----

type stubValidator struct {
	results []error // results[i] is returned on the i-th Validate call
	calls   int
}

func (v *stubValidator) Validate(_ context.Context, _ string) error {
	i := v.calls
	v.calls++
	if i < len(v.results) {
		return v.results[i]
	}
	return nil
}

// ---- test helpers ----

// runAgent sets up an in-memory session and runner, then collects all events.
// It fails the test if the runner returns an error.
func runAgent(t *testing.T, ag agent.Agent, initialState map[string]any, userMsg string) []*session.Event {
	t.Helper()
	ctx := t.Context()

	svc := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "test",
		Agent:          ag,
		SessionService: svc,
	})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}

	_, err = svc.Create(ctx, &session.CreateRequest{
		AppName:   "test",
		UserID:    "user",
		SessionID: "sess",
		State:     initialState,
	})
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}

	var events []*session.Event
	for event, err := range r.Run(ctx, "user", "sess", genai.NewContentFromText(userMsg, genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatalf("runner.Run error: %v", err)
		}
		if event != nil {
			events = append(events, event)
		}
	}
	return events
}

// runAgentCollectErrors collects both events and errors from a runner run.
func runAgentCollectErrors(t *testing.T, ag agent.Agent, initialState map[string]any, userMsg string) ([]*session.Event, []error) {
	t.Helper()
	ctx := t.Context()

	svc := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "test",
		Agent:          ag,
		SessionService: svc,
	})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}

	_, err = svc.Create(ctx, &session.CreateRequest{
		AppName:   "test",
		UserID:    "user",
		SessionID: "sess",
		State:     initialState,
	})
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}

	var events []*session.Event
	var errs []error
	for event, runErr := range r.Run(ctx, "user", "sess", genai.NewContentFromText(userMsg, genai.RoleUser), agent.RunConfig{}) {
		if runErr != nil {
			errs = append(errs, runErr)
			continue
		}
		if event != nil {
			events = append(events, event)
		}
	}
	return events, errs
}

// ---- GeneratorAgent tests ----

// TestGeneratorAgent_Success: provider returns output → event contains that text.
func TestGeneratorAgent_Success(t *testing.T) {
	local := &stubProvider{name: "local", output: "package main\n\nfunc Hello() {}"}
	gen, err := adapters.NewGeneratorAgent(local, nil)
	if err != nil {
		t.Fatalf("NewGeneratorAgent: %v", err)
	}

	events := runAgent(t, gen, nil, "write a Hello function")

	if len(events) == 0 {
		t.Fatal("expected at least one event, got none")
	}

	// The last non-user event should contain the generated code.
	last := events[len(events)-1]
	if last.LLMResponse.Content == nil {
		t.Fatal("expected event to have LLMResponse.Content, got nil")
	}

	var got string
	for _, part := range last.LLMResponse.Content.Parts {
		got += part.Text
	}

	if !strings.Contains(got, "Hello") {
		t.Errorf("expected event content to contain 'Hello', got: %q", got)
	}

	if local.calls != 1 {
		t.Errorf("expected provider to be called once, got %d", local.calls)
	}
}

// TestGeneratorAgent_UsesInstruction: provider receives the user message as input.
func TestGeneratorAgent_UsesInstruction(t *testing.T) {
	local := &stubProvider{name: "local", output: "generated"}
	gen, err := adapters.NewGeneratorAgent(local, nil)
	if err != nil {
		t.Fatalf("NewGeneratorAgent: %v", err)
	}

	runAgent(t, gen, nil, "the specific instruction text")

	if !strings.Contains(local.lastInput, "the specific instruction text") {
		t.Errorf("expected provider input to contain the user instruction, got: %q", local.lastInput)
	}
}

// TestGeneratorAgent_Error: provider error → runner returns an error.
func TestGeneratorAgent_Error(t *testing.T) {
	local := &stubProvider{name: "local", err: errors.New("model unavailable")}
	gen, err := adapters.NewGeneratorAgent(local, nil)
	if err != nil {
		t.Fatalf("NewGeneratorAgent: %v", err)
	}

	_, errs := runAgentCollectErrors(t, gen, nil, "some instruction")

	if len(errs) == 0 {
		t.Error("expected an error when provider fails, got none")
	}
}

// TestGeneratorAgent_SelectsLocalByDefault: without use_cloud state, local is used.
func TestGeneratorAgent_SelectsLocalByDefault(t *testing.T) {
	local := &stubProvider{name: "local", output: "local output"}
	cloud := &stubProvider{name: "cloud", output: "cloud output"}
	gen, err := adapters.NewGeneratorAgent(local, cloud)
	if err != nil {
		t.Fatalf("NewGeneratorAgent: %v", err)
	}

	runAgent(t, gen, nil, "instruction")

	if local.calls != 1 {
		t.Errorf("expected local provider called once, got %d", local.calls)
	}
	if cloud.calls != 0 {
		t.Errorf("expected cloud provider not called, got %d calls", cloud.calls)
	}
}

// TestGeneratorAgent_SelectsCloudWhenStateSet: with use_cloud=true, cloud is used.
func TestGeneratorAgent_SelectsCloudWhenStateSet(t *testing.T) {
	local := &stubProvider{name: "local", output: "local output"}
	cloud := &stubProvider{name: "cloud", output: "cloud output"}
	gen, err := adapters.NewGeneratorAgent(local, cloud)
	if err != nil {
		t.Fatalf("NewGeneratorAgent: %v", err)
	}

	runAgent(t, gen, map[string]any{adapters.StateKeyUseCloud: true}, "instruction")

	if local.calls != 0 {
		t.Errorf("expected local provider not called, got %d calls", local.calls)
	}
	if cloud.calls != 1 {
		t.Errorf("expected cloud provider called once, got %d", cloud.calls)
	}
}

// ---- ValidatorAgent tests ----

// TestValidatorAgent_Success_Escalates: validation passes → event has Escalate=true.
func TestValidatorAgent_Success_Escalates(t *testing.T) {
	val := &stubValidator{results: []error{nil}}
	ag, err := adapters.NewValidatorAgent(val, t.TempDir(), 3)
	if err != nil {
		t.Fatalf("NewValidatorAgent: %v", err)
	}

	events := runAgent(t, ag, nil, "trigger")

	if len(events) == 0 {
		t.Fatal("expected at least one event, got none")
	}

	// Find the validator's event (author "validator")
	var found bool
	for _, ev := range events {
		if ev.Author == "validator" && ev.Actions.Escalate {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a validator event with Escalate=true, none found")
	}
}

// TestValidatorAgent_Failure_NoEscalate: validation fails → no escalate, error content emitted.
func TestValidatorAgent_Failure_NoEscalate(t *testing.T) {
	val := &stubValidator{results: []error{errors.New("build failed: undefined: Foo")}}
	ag, err := adapters.NewValidatorAgent(val, t.TempDir(), 3)
	if err != nil {
		t.Fatalf("NewValidatorAgent: %v", err)
	}

	// Run with MaxIterations=1 so the loop terminates after one round.
	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: 1,
		AgentConfig: agent.Config{
			Name:      "loop",
			SubAgents: []agent.Agent{ag},
		},
	})
	if err != nil {
		t.Fatalf("loopagent.New: %v", err)
	}

	events := runAgent(t, loop, nil, "trigger")

	for _, ev := range events {
		if ev.Author == "validator" && ev.Actions.Escalate {
			t.Error("expected no Escalate=true on validation failure")
		}
	}

	// Validator should have been called once.
	if val.calls != 1 {
		t.Errorf("expected validator called once, got %d", val.calls)
	}

	// Event should contain the error text.
	var errorFound bool
	for _, ev := range events {
		if ev.Author == "validator" {
			for _, part := range ev.LLMResponse.Content.Parts {
				if strings.Contains(part.Text, "build failed") {
					errorFound = true
				}
			}
		}
	}
	if !errorFound {
		t.Error("expected a validator event containing the error message")
	}
}

// TestValidatorAgent_FailureCounter_Increments: failure counter is incremented in session state.
func TestValidatorAgent_FailureCounter_Increments(t *testing.T) {
	val := &stubValidator{results: []error{
		errors.New("first failure"),
		errors.New("second failure"),
		nil, // third call succeeds
	}}
	ag, err := adapters.NewValidatorAgent(val, t.TempDir(), 10) // threshold=10 so no escalate
	if err != nil {
		t.Fatalf("NewValidatorAgent: %v", err)
	}

	// Wrap in a LoopAgent so ValidatorAgent runs multiple times.
	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: 2,
		AgentConfig: agent.Config{
			Name:      "loop",
			SubAgents: []agent.Agent{ag},
		},
	})
	if err != nil {
		t.Fatalf("loopagent.New: %v", err)
	}

	runAgent(t, loop, nil, "trigger")

	if val.calls != 2 {
		t.Errorf("expected validator called twice, got %d", val.calls)
	}

	// After 2 failures, the failure counter in state should be 2.
	// We verify this indirectly by checking that subsequent escalation
	// behaviour would be triggered at the right count (see escalation test).
}

// ---- Escalation state integration test ----

// TestEscalationState_SwitchesToCloudAfterThreshold: after N validation failures
// the GeneratorAgent switches from local to cloud provider.
func TestEscalationState_SwitchesToCloudAfterThreshold(t *testing.T) {
	local := &stubProvider{name: "local", output: "local output"}
	cloud := &stubProvider{name: "cloud", output: "cloud output"}

	// Validator fails once then succeeds.
	val := &stubValidator{results: []error{
		errors.New("build failed"),
		nil, // second call succeeds
	}}

	// Threshold = 1: switch to cloud after 1 failure.
	gen, err := adapters.NewGeneratorAgent(local, cloud)
	if err != nil {
		t.Fatalf("NewGeneratorAgent: %v", err)
	}
	valAg, err := adapters.NewValidatorAgent(val, t.TempDir(), 1)
	if err != nil {
		t.Fatalf("NewValidatorAgent: %v", err)
	}

	// LoopAgent runs: Gen → Val (fail) → Gen → Val (success/escalate).
	// MaxIterations=5 is a safety cap; the loop should exit after 2 via Escalate.
	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: 5, // exits on escalate well before this cap
		AgentConfig: agent.Config{
			Name:      "loop",
			SubAgents: []agent.Agent{gen, valAg},
		},
	})
	if err != nil {
		t.Fatalf("loopagent.New: %v", err)
	}

	runAgent(t, loop, nil, "write code")

	// Iteration 1: local provider should have been called.
	if local.calls == 0 {
		t.Error("expected local provider to be called in iteration 1")
	}

	// Iteration 2: after 1 failure (>= threshold 1), cloud should have been called.
	if cloud.calls == 0 {
		t.Error("expected cloud provider to be called in iteration 2 after escalation threshold")
	}

	// The loop should have terminated (validator called exactly 2 times).
	if val.calls != 2 {
		t.Errorf("expected validator called 2 times, got %d", val.calls)
	}
}
