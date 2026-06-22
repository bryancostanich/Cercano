package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/engine/ollama"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/testfixtures"
	"cercano/source/server/internal/tools"
)

// TestSandbox_GenerateAndRunTests verifies the agent can generate passing tests for a simple sandbox project.
func TestSandbox_GenerateAndRunTests(t *testing.T) {
	if os.Getenv("SANDBOX_TEST") != "1" {
		t.Skip("Skipping sandbox test; set SANDBOX_TEST=1 to run")
	}

	// Copy the needs-tests fixture into a per-test sandbox the test can mutate.
	sandboxDir := testfixtures.Copy(t, "go/needs-tests")
	targetFile := filepath.Join(sandboxDir, "calculator.go")

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("Failed to read calculator.go: %v", err)
	}

	provider := legacymodels.NewLocalModelProvider(ollama.NewOllamaEngine("http://localhost:11434"), "qwen3-coder")
	handler := tools.NewGenericGenerator(provider)
	validator := tools.NewGoValidator()
	coordinator := loop.NewGenerationCoordinator(handler, handler, validator)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	t.Log("Generating and verifying tests for calculator.go (with self-correction)...")
	finalCode, err := coordinator.Coordinate(ctx, "Write table-driven unit tests for the following Go code using the standard 'testing' package.", string(content), sandboxDir, "calculator_test.go", nil)
	if err != nil {
		t.Fatalf("Generation/Self-Correction failed: %v", err)
	}

	t.Logf("Successfully generated and verified tests:\n%s", finalCode.Output)
}
