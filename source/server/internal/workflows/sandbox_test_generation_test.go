package workflows_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/workflows"
)

// TestSandbox_GenerateAndRunTests verifies the agent can generate passing tests for a simple sandbox project.
func TestSandbox_GenerateAndRunTests(t *testing.T) {
	if os.Getenv("SANDBOX_TEST") != "1" {
		t.Skip("Skipping sandbox test; set SANDBOX_TEST=1 to run")
	}

	// 1. Setup paths
	wd, _ := os.Getwd()
	// wd is .../source/server/internal/workflows
	// We need to go up 4 levels to get to root (source/server/internal/workflows -> server -> source -> root -> test/sandbox)
	// No, Wait. 
	// root/source/server/internal/workflows
	// root/test/sandbox
	// So ../../../.. is correct.
	
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
handler := capabilities.NewUnitTestHandler(provider)
validator := capabilities.NewGoTestValidator()
coordinator := workflows.NewGenerationCoordinator(handler, validator)

	// 4. Generate and Verify Tests with Self-Correction
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second) // Increased timeout for retries
	defer cancel()

	t.Log("Generating and verifying tests for calculator.go (with self-correction)...")
	finalCode, err := coordinator.Coordinate(ctx, string(content), sandboxDir, "calculator_test.go")
	if err != nil {
		t.Fatalf("Generation/Self-Correction failed: %v", err)
	}

	t.Logf("Successfully generated and verified tests:\n%s", finalCode)
}