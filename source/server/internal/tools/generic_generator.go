package tools

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/agent"
)

// GenericGenerator handles requests to generate and fix code based on instructions.
type GenericGenerator struct {
	provider agent.ModelProvider
}

// NewGenericGenerator creates a new GenericGenerator with the given provider.
func NewGenericGenerator(provider agent.ModelProvider) *GenericGenerator {
	return &GenericGenerator{provider: provider}
}

// Generate generates code based on the instruction and provided Go code.
func (h *GenericGenerator) Generate(ctx context.Context, instruction string, code string) (string, error) {
	if instruction == "" {
		return "", fmt.Errorf("instruction cannot be empty")
	}

	// Construct the prompt with specific instructions for the model
	prompt := fmt.Sprintf(`You are an expert Go developer.
Instruction: %s
Return ONLY the requested Go code. Do not include any explanations.

Code Context:
`+"```go\n%s\n```"+`
`,
		instruction, code)

	req := &agent.Request{
		Input: prompt,
	}

	resp, err := h.provider.Process(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to generate code: %w", err)
	}

	return cleanMarkdown(resp.Output), nil
}

// Fix attempts to fix the provided code based on the error message.
func (h *GenericGenerator) Fix(ctx context.Context, code string, errorMsg string) (string, error) {
	prompt := fmt.Sprintf(`You are an expert Go developer.
The following Go code has errors. Please fix it according to the error message.
Return ONLY the corrected Go code. Do not explain.

Code:
`+"```go\n%s\n```"+`

Error:
%s
`, code, errorMsg)

	req := &agent.Request{Input: prompt}
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
