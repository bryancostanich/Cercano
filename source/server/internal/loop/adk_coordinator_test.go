package loop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentmod "cercano/source/server/internal/agent"
	"cercano/source/server/internal/loop"
)

// ---- stubs for ADK coordinator tests ----

// seqProvider returns outputs in order; repeats the last one when exhausted.
type seqProvider struct {
	name    string
	outputs []string
	calls   int
}

func (p *seqProvider) Name() string { return p.name }

func (p *seqProvider) Process(_ context.Context, req *agentmod.Request) (*agentmod.Response, error) {
	i := p.calls
	p.calls++
	if i < len(p.outputs) {
		return &agentmod.Response{Output: p.outputs[i]}, nil
	}
	last := p.outputs[len(p.outputs)-1]
	return &agentmod.Response{Output: last}, nil
}

// funcValidator is a Validator whose behaviour is controlled by a closure.
type funcValidator struct {
	fn    func(ctx context.Context, workDir string) error
	calls int
}

func (v *funcValidator) Validate(ctx context.Context, workDir string) error {
	v.calls++
	return v.fn(ctx, workDir)
}

// ---- ADK coordinator tests ----

// TestADKCoordinator_SuccessFirstTime: generator succeeds on first pass.
func TestADKCoordinator_SuccessFirstTime(t *testing.T) {
	// Call 0 = filename inference → "not a filename" (space → rejected)
	// Call 1 = first loop iteration → "generated code"
	local := &seqProvider{name: "local", outputs: []string{"not a filename", "generated code"}}
	val := &funcValidator{fn: func(_ context.Context, _ string) error { return nil }}

	coord := loop.NewADKCoordinator(local, nil, val)
	workDir := t.TempDir()

	result, err := coord.Coordinate(context.Background(), "write a function", "", workDir, "main.go", nil)

	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !strings.Contains(result.Output, "generated code") {
		t.Errorf("expected output to contain 'generated code', got: %q", result.Output)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	if result.FileChanges[0].Content != "generated code" {
		t.Errorf("expected file change content 'generated code', got %q", result.FileChanges[0].Content)
	}
	if result.FileChanges[0].Path != "main.go" {
		t.Errorf("expected path 'main.go', got %q", result.FileChanges[0].Path)
	}
}

// TestADKCoordinator_FixSuccess: first generation fails validation; second (fix) succeeds.
func TestADKCoordinator_FixSuccess(t *testing.T) {
	// Call 0 = inference (ignored), Call 1 = gen → "bad code", Call 2 = fix → "fixed code"
	local := &seqProvider{name: "local", outputs: []string{"not a filename", "bad code", "fixed code"}}

	valCalls := 0
	val := &funcValidator{fn: func(_ context.Context, _ string) error {
		valCalls++
		if valCalls < 2 {
			return errors.New("compilation error: undefined: Foo")
		}
		return nil
	}}

	coord := loop.NewADKCoordinator(local, nil, val)
	workDir := t.TempDir()

	result, err := coord.Coordinate(context.Background(), "instruction", "", workDir, "main.go", nil)

	if err != nil {
		t.Fatalf("expected success after fix, got error: %v", err)
	}
	if !strings.Contains(result.Output, "fixed code") {
		t.Errorf("expected output to contain 'fixed code', got: %q", result.Output)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	if result.FileChanges[0].Content != "fixed code" {
		t.Errorf("expected file change content 'fixed code', got %q", result.FileChanges[0].Content)
	}
}

// TestADKCoordinator_Escalation: after local fails, cloud is used and succeeds.
func TestADKCoordinator_Escalation(t *testing.T) {
	// local: inference (ignored) + bad gen
	local := &seqProvider{name: "local", outputs: []string{"not a filename", "bad local code"}}

	cloudCalled := false
	cloud := &seqProvider{name: "cloud", outputs: []string{"cloud good code"}}

	val := &funcValidator{fn: func(_ context.Context, _ string) error {
		if !cloudCalled {
			return errors.New("compilation error")
		}
		return nil
	}}

	coord := loop.NewADKCoordinator(local, cloud, val)
	coord.SetEscalationThreshold(1) // switch after 1 failure

	// Intercept cloud calls to set flag.
	// We do this by wrapping cloud.Process via an adapter below.
	// Since seqProvider doesn't support callbacks, we use a wrapper.
	wrappedCloud := &callTrackingProvider{inner: cloud, onCall: func() { cloudCalled = true }}

	coord2 := loop.NewADKCoordinator(local, wrappedCloud, val)
	coord2.SetEscalationThreshold(1)

	workDir := t.TempDir()
	result, err := coord2.Coordinate(context.Background(), "instruction", "", workDir, "main.go", nil)

	if err != nil {
		t.Fatalf("expected success after escalation, got error: %v", err)
	}
	if !cloudCalled {
		t.Error("expected cloud provider to be called after escalation")
	}
	if !strings.Contains(result.Output, "cloud good code") {
		t.Errorf("expected output to contain 'cloud good code', got: %q", result.Output)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	if result.RoutingMetadata.Escalated != true {
		t.Error("expected RoutingMetadata.Escalated to be true")
	}
	_ = coord // suppress unused warning; coord2 is used above
}

// callTrackingProvider wraps a ModelProvider and invokes a callback on each Process call.
type callTrackingProvider struct {
	inner  agentmod.ModelProvider
	onCall func()
}

func (p *callTrackingProvider) Name() string { return p.inner.Name() }
func (p *callTrackingProvider) Process(ctx context.Context, req *agentmod.Request) (*agentmod.Response, error) {
	p.onCall()
	return p.inner.Process(ctx, req)
}

// TestADKCoordinator_MaxRetriesExceeded: all validation attempts fail; coordinator
// returns a failure response without an error.
func TestADKCoordinator_MaxRetriesExceeded(t *testing.T) {
	local := &seqProvider{name: "local", outputs: []string{"bad code"}} // repeats last
	val := &funcValidator{fn: func(_ context.Context, _ string) error {
		return errors.New("build always fails")
	}}

	coord := loop.NewADKCoordinator(local, nil, val)
	workDir := t.TempDir()

	result, err := coord.Coordinate(context.Background(), "instruction", "", workDir, "main.go", nil)

	if err != nil {
		t.Fatalf("expected graceful failure (no error), got: %v", err)
	}
	if len(result.FileChanges) != 0 {
		t.Errorf("expected no FileChanges on failure, got %d", len(result.FileChanges))
	}
	if result.Output == "" {
		t.Error("expected non-empty failure message in Output")
	}
}

// TestADKCoordinator_BackupRestored: when all attempts fail the original file is restored.
func TestADKCoordinator_BackupRestored(t *testing.T) {
	workDir := t.TempDir()
	targetFile := filepath.Join(workDir, "main.go")
	originalContent := "package main\n\nfunc original() {}"
	if err := os.WriteFile(targetFile, []byte(originalContent), 0644); err != nil {
		t.Fatal(err)
	}

	local := &seqProvider{name: "local", outputs: []string{"generated but bad code"}}
	val := &funcValidator{fn: func(_ context.Context, _ string) error {
		return errors.New("always fails")
	}}

	coord := loop.NewADKCoordinator(local, nil, val)
	_, err := coord.Coordinate(context.Background(), "instruction", "", workDir, "main.go", nil)
	if err != nil {
		t.Fatalf("expected graceful failure, got: %v", err)
	}

	restored, readErr := os.ReadFile(targetFile)
	if readErr != nil {
		t.Fatalf("expected file to exist after restore, got: %v", readErr)
	}
	if string(restored) != originalContent {
		t.Errorf("expected original content %q, got %q", originalContent, string(restored))
	}
}

// TestADKCoordinator_InfersFilename: when the model returns a valid filename for the
// inference prompt, the coordinator uses that name.
func TestADKCoordinator_InfersFilename(t *testing.T) {
	local := &seqProvider{name: "local", outputs: []string{
		"inferred_test.go", // inference → accepted (no space, has dot, different from source.go)
		"generated code",   // actual generation
	}}
	val := &funcValidator{fn: func(_ context.Context, _ string) error { return nil }}

	coord := loop.NewADKCoordinator(local, nil, val)
	workDir := t.TempDir()

	result, err := coord.Coordinate(context.Background(), "Generate tests", "", workDir, "source.go", nil)

	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	if result.FileChanges[0].Path != "inferred_test.go" {
		t.Errorf("expected path 'inferred_test.go', got %q", result.FileChanges[0].Path)
	}
}

// TestADKCoordinator_ProgressReported: progress callback is called during execution.
func TestADKCoordinator_ProgressReported(t *testing.T) {
	local := &seqProvider{name: "local", outputs: []string{"not a filename", "generated code"}}
	val := &funcValidator{fn: func(_ context.Context, _ string) error { return nil }}

	coord := loop.NewADKCoordinator(local, nil, val)
	workDir := t.TempDir()

	var messages []string
	progress := func(msg string) { messages = append(messages, msg) }

	_, err := coord.Coordinate(context.Background(), "instruction", "", workDir, "main.go", progress)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if len(messages) == 0 {
		t.Error("expected at least one progress message, got none")
	}
}
