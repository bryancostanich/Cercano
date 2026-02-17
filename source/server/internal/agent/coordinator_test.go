package agent_test

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/agent"
)

type MockGenerator struct {
	GenerateFunc func(ctx context.Context, code string) (string, error)
	FixFunc      func(ctx context.Context, code string, errorMsg string) (string, error)
}

func (m *MockGenerator) Generate(ctx context.Context, code string) (string, error) {
	return m.GenerateFunc(ctx, code)
}

func (m *MockGenerator) Fix(ctx context.Context, code string, errorMsg string) (string, error) {
	return m.FixFunc(ctx, code, errorMsg)
}

type MockValidator struct {
	ValidateFunc func(ctx context.Context, workDir string) error
}

func (m *MockValidator) Validate(ctx context.Context, workDir string) error {
	return m.ValidateFunc(ctx, workDir)
}

func TestGenerationCoordinator_Coordinate_SuccessFirstTime(t *testing.T) {
	gen := &MockGenerator{
		GenerateFunc: func(ctx context.Context, code string) (string, error) {
			return "generated code", nil
		},
	}
	val := &MockValidator{
		ValidateFunc: func(ctx context.Context, workDir string) error {
			return nil
		},
	}

	coordinator := agent.NewGenerationCoordinator(gen, val)
	
	ctx := context.Background()
	workDir := t.TempDir()
	result, err := coordinator.Coordinate(ctx, "input code", workDir, "test_file.go")
	
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result != "generated code" {
		t.Errorf("expected 'generated code', got '%s'", result)
	}
}

func TestGenerationCoordinator_Coordinate_FixSuccess(t *testing.T) {
	fixCalled := false
	gen := &MockGenerator{
		GenerateFunc: func(ctx context.Context, code string) (string, error) {
			return "bad code", nil
		},
		FixFunc: func(ctx context.Context, code string, errorMsg string) (string, error) {
			fixCalled = true
			return "fixed code", nil
		},
	}
	val := &MockValidator{
		ValidateFunc: func(ctx context.Context, workDir string) error {
			if !fixCalled {
				return errors.New("compilation error")
			}
			return nil
		},
	}

	coordinator := agent.NewGenerationCoordinator(gen, val)
	ctx := context.Background()
	workDir := t.TempDir()
	result, err := coordinator.Coordinate(ctx, "input code", workDir, "test_file.go")

	if err != nil {
		t.Fatalf("expected success after fix, got %v", err)
	}
	if !fixCalled {
		t.Error("expected Fix to be called")
	}
	if result != "fixed code" {
		t.Errorf("expected 'fixed code', got '%s'", result)
	}
}
