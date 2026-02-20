package loop_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/loop"
)

type MockGenerator struct {
	GenerateFunc func(ctx context.Context, instruction, code string) (string, error)
	FixFunc      func(ctx context.Context, code string, errorMsg string) (string, error)
}

func (m *MockGenerator) Generate(ctx context.Context, instruction, code string) (string, error) {
	return m.GenerateFunc(ctx, instruction, code)
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
		GenerateFunc: func(ctx context.Context, instruction, code string) (string, error) {
			return "generated code", nil
		},
	}
	val := &MockValidator{
		ValidateFunc: func(ctx context.Context, workDir string) error {
			return nil
		},
	}

	// New constructor will take two generators
	coordinator := loop.NewGenerationCoordinator(gen, gen, val)
	
	ctx := context.Background()
	workDir := t.TempDir()
	result, err := coordinator.Coordinate(ctx, "instruction", "input code", workDir, "test_file.go", nil)
	
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(result.Output, "generated code") {
		t.Errorf("expected output to contain 'generated code', got '%s'", result.Output)
	}
	if result.FileChanges[0].Content != "generated code" {
		t.Errorf("expected file change content 'generated code', got '%s'", result.FileChanges[0].Content)
	}
}

func TestGenerationCoordinator_Coordinate_FixSuccess(t *testing.T) {
	fixCalled := false
	gen := &MockGenerator{
		GenerateFunc: func(ctx context.Context, instruction, code string) (string, error) {
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

	coordinator := loop.NewGenerationCoordinator(gen, gen, val)
	ctx := context.Background()
	workDir := t.TempDir()
	result, err := coordinator.Coordinate(ctx, "instruction", "input code", workDir, "test_file.go", nil)

	if err != nil {
		t.Fatalf("expected success after fix, got %v", err)
	}
	if !fixCalled {
		t.Error("expected Fix to be called")
	}
	if !strings.Contains(result.Output, "fixed code") {
		t.Errorf("expected output to contain 'fixed code', got '%s'", result.Output)
	}
	if result.FileChanges[0].Content != "fixed code" {
		t.Errorf("expected file change content 'fixed code', got '%s'", result.FileChanges[0].Content)
	}
}

func TestGenerationCoordinator_Coordinate_Escalation(t *testing.T) {
	localAttempts := 0
	cloudFixed := false

	localGen := &MockGenerator{
		GenerateFunc: func(ctx context.Context, instruction, code string) (string, error) {
			return "local bad code", nil
		},
		FixFunc: func(ctx context.Context, code string, errorMsg string) (string, error) {
			localAttempts++
			return "local bad code fix", nil
		},
	}

	cloudGen := &MockGenerator{
		FixFunc: func(ctx context.Context, code string, errorMsg string) (string, error) {
			cloudFixed = true
			return "cloud good code", nil
		},
	}

	val := &MockValidator{
		ValidateFunc: func(ctx context.Context, workDir string) error {
			if !cloudFixed {
				return errors.New("compilation error")
			}
			return nil
		},
	}

	// Coordinator with 2 local attempts limit (1 initial + 1 fix)
	// Escalation threshold = 2.
	// 1st attempt: Local Generate -> Fail
	// 2nd attempt: Local Fix -> Fail
	// 3rd attempt: Cloud Fix -> Success
	coordinator := loop.NewGenerationCoordinator(localGen, cloudGen, val)
	coordinator.SetEscalationThreshold(2)

	ctx := context.Background()
	workDir := t.TempDir()
	result, err := coordinator.Coordinate(ctx, "instruction", "input code", workDir, "test_file.go", nil)

	if err != nil {
		t.Fatalf("expected success after escalation, got %v", err)
	}
	if localAttempts != 1 {
		t.Errorf("expected 1 local fix attempt, got %d", localAttempts)
	}
	if !cloudFixed {
		t.Error("expected cloud generator to be used")
	}
	if !strings.Contains(result.Output, "cloud good code") {
		t.Errorf("expected output to contain 'cloud good code', got '%s'", result.Output)
	}
	if result.FileChanges[0].Content != "cloud good code" {
		t.Errorf("expected file change content 'cloud good code', got '%s'", result.FileChanges[0].Content)
	}
}

func TestGenerationCoordinator_Coordinate_InfersFilename(t *testing.T) {
	gen := &MockGenerator{
		GenerateFunc: func(ctx context.Context, instruction, code string) (string, error) {
			if strings.Contains(instruction, "Return ONLY the filename") {
				return "inferred_test.go", nil
			}
			return "generated code", nil
		},
	}
	val := &MockValidator{
		ValidateFunc: func(ctx context.Context, workDir string) error {
			return nil
		},
	}

	coordinator := loop.NewGenerationCoordinator(gen, gen, val)
	ctx := context.Background()
	workDir := t.TempDir()
	
	// We pass "source.go" but expect it to infer "inferred_test.go"
	result, err := coordinator.Coordinate(ctx, "Generate tests", "", workDir, "source.go", nil)

	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	
	if result.FileChanges[0].Path != "inferred_test.go" {
		t.Errorf("expected path 'inferred_test.go', got '%s'", result.FileChanges[0].Path)
	}
}
