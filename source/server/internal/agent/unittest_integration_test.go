package agent_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
)

// Use the same model as the other integration tests
const integrationTestModelName = "qwen3-coder" 

func TestUnitTestHandler_Integration_GenerateValidCode(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test; set INTEGRATION_TEST=1 to run")
	}

	// Assume Ollama is running at localhost:11434
	// We use a known small coding model. Ensure this model is pulled in Ollama.
	provider := llm.NewOllamaProvider(integrationTestModelName, "http://localhost:11434")
	handler := agent.NewUnitTestHandler(provider)

	// Input: A simple Go function
	inputCode := `
package mypkg

func Add(a, b int) int {
	return a + b
}
`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	generatedCode, err := handler.Generate(ctx, inputCode)
	if err != nil {
		t.Fatalf("Integration test failed to generate code: %v", err)
	}

	if generatedCode == "" {
		t.Fatal("Generated code is empty")
	}

	t.Logf("Generated Code:\n%s", generatedCode)

	// Quality Check 1: Does it parse as valid Go?
	fset := token.NewFileSet()
	// Wrap in a file structure if the model returns just the func
	// Usually the prompt asks for "just the Go code", which might include package declaration.
	// If the model creates a snippet without package, ParseFile might complain if we don't handle it.
	// However, usually tests start with `package ...`.
	
	// Clean up any markdown code blocks if the model ignored instructions (common with smaller models)
	cleanedCode := cleanMarkdown(generatedCode)
	
f, err := parser.ParseFile(fset, "", cleanedCode, parser.ParseComments)
	if err != nil {
		t.Fatalf("Generated code failed to parse as Go: %v\nCode:\n%s", err, cleanedCode)
	}

	// Quality Check 2: Does it import "testing"?
	hasTesting := false
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, "\"") == "testing" {
			hasTesting = true
			break
		}
	}
	if !hasTesting {
		t.Error("Generated code does not import 'testing' package")
	}

	// Quality Check 3: Does it have a Test function?
	hasTestFunc := false
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if strings.HasPrefix(fn.Name.Name, "Test") {
				hasTestFunc = true
				break
			}
		}
	}
	if !hasTestFunc {
		t.Error("Generated code does not contain a function starting with 'Test'")
	}
}

// cleanMarkdown removes ```go and ``` lines if present
func cleanMarkdown(code string) string {
	lines := strings.Split(code, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}
