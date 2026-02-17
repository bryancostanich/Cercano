package agent

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/router"
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
IMPORTANT: Do NOT blindly copy imports from the source code. Only import packages that are strictly necessary for the TEST code (like 'testing').

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

	return cleanMarkdown(resp.Output), nil
}

// Fix attempts to fix the provided code based on the error message.
func (h *UnitTestHandler) Fix(ctx context.Context, code string, errorMsg string) (string, error) {
	prompt := fmt.Sprintf(`You are an expert Go developer.
The following Go code has errors. Please fix it.
Return ONLY the corrected Go code. Do not explain.

Code:
`+"```go\n%s\n```"+`

Error:
%s
`, code, errorMsg)

	req := &router.Request{Input: prompt}
	resp, err := h.provider.Process(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to fix code: %w", err)
	}

	return cleanMarkdown(resp.Output), nil
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
