package agent

import (
	"context"
	"fmt"

	"cercano/source/internal/router"
)

// UnitTestHandler handles requests to generate unit tests.
type UnitTestHandler struct {
	provider router.ModelProvider
}

// NewUnitTestHandler creates a new UnitTestHandler with the given provider.
func NewUnitTestHandler(provider router.ModelProvider) *UnitTestHandler {
	return &UnitTestHandler{provider: provider}
}

// Generate generates unit tests for the provided Go code.
func (h *UnitTestHandler) Generate(ctx context.Context, code string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("input code cannot be empty")
	}

	// Construct the prompt with specific instructions for the model
	prompt := fmt.Sprintf(`You are an expert Go developer.
Write table-driven unit tests for the following Go code using the standard 'testing' package.
Ensure the tests cover happy paths and edge cases.
Do not include any explanations, just the Go code.

Code:
`+"```go\n%s\n```"+`
`,
		code) // Note: The original prompt had an extra backtick here, which has been removed.

	req := &router.Request{
		Input: prompt,
	}

	resp, err := h.provider.Process(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to generate tests: %w", err)
	}

	return resp.Output, nil
}
