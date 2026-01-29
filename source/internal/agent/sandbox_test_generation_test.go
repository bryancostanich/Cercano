package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/internal/agent"
	"cercano/source/internal/llm"
)

// TestSandbox_GenerateAndRunTests verifies the agent can generate passing tests for a simple sandbox project.
func TestSandbox_GenerateAndRunTests(t *testing.T) {
	if os.Getenv("SANDBOX_TEST") != "1" {
		t.Skip("Skipping sandbox test; set SANDBOX_TEST=1 to run")
	}

	// 1. Setup paths
	wd, _ := os.Getwd()
	// wd is .../source/internal/agent
	// We need to go up 3 levels to get to root, then into test/sandbox
	sandboxDir := filepath.Join(wd, "../../..", "test", "sandbox")
	targetFile := filepath.Join(sandboxDir, "calculator.go")
	outputFile := filepath.Join(sandboxDir, "calculator_test.go")

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Fatalf("Sandbox file not found at: %s", targetFile)
	}

	// 2. Read Target Code
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("Failed to read calculator.go: %v", err)
	}

	// 3. Initialize Agent
	// Using qwen3-coder
	provider := llm.NewOllamaProvider("qwen3-coder", "http://localhost:11434")
	handler := agent.NewUnitTestHandler(provider)

	// 4. Generate Tests
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	t.Log("Generating tests for calculator.go...")
	generatedCode, err := handler.Generate(ctx, string(content))
	if err != nil {
		t.Fatalf("Generation failed: %v", err)
	}

	// 5. Write Generated Tests
	err = os.WriteFile(outputFile, []byte(generatedCode), 0644)
	if err != nil {
		t.Fatalf("Failed to write calculator_test.go: %v", err)
	}
	t.Logf("Wrote generated tests to %s", outputFile)

	// 6. Run the Generated Tests
	// We execute 'go test' inside the sandbox directory
	cmd := exec.Command("go", "test", "-v", ".")
	cmd.Dir = sandboxDir
	output, err := cmd.CombinedOutput()

	t.Logf("--- SANDBOX TEST OUTPUT ---\n%s\n--------------------------- ", string(output))

	if err != nil {
		t.Fatalf("Generated tests failed to pass: %v", err)
	}

	t.Log("SUCCESS: Generated tests compiled and passed!")
}
