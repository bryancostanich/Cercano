package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/tools"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/loop"
)

// TestSandbox_GenerateAndRunTests verifies the agent can generate passing tests for a simple sandbox project.
func TestSandbox_GenerateAndRunTests(t *testing.T) {
	if os.Getenv("SANDBOX_TEST") != "1" {
		t.Skip("Skipping sandbox test; set SANDBOX_TEST=1 to run")
	}

	// 1. Setup paths
	wd, _ := os.Getwd()
	// wd is .../source/server/test/integration
sandboxDir := filepath.Join(wd, "../../../..", "test", "sandbox")
targetFile := filepath.Join(sandboxDir, "calculator.go")

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Fatalf("Sandbox file not found at: %s (wd: %s)", targetFile, wd)
	}

	// 2. Read Target Code
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("Failed to read calculator.go: %v", err)
	}

	// 3. Initialize Agent Components
	provider := llm.NewOllamaProvider("qwen3-coder", "http://localhost:11434")
	handler := tools.NewGenericGenerator(provider)
	validator := tools.NewGoValidator()
	coordinator := loop.NewGenerationCoordinator(handler, handler, validator)

	// 4. Generate and Verify Tests with Self-Correction
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second) // Increased timeout for retries
	defer cancel()

	t.Log("Generating and verifying tests for calculator.go (with self-correction)...")
	finalCode, err := coordinator.Coordinate(ctx, "Write table-driven unit tests for the following Go code using the standard 'testing' package.", string(content), sandboxDir, "calculator_test.go")
	if err != nil {
		t.Fatalf("Generation/Self-Correction failed: %v", err)
	}

	t.Logf("Successfully generated and verified tests:\n%s", finalCode.Output)
}
