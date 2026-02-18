package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/tools"
)

// GenerationCoordinator orchestrates the iterative generation and validation of code.
type GenerationCoordinator struct {
	localGenerator      tools.CodeGenerator
	cloudGenerator      tools.CodeGenerator
	validator           tools.Validator
	maxRetries          int
	escalationThreshold int
}

// NewGenerationCoordinator creates a new coordinator with local and cloud generators.
func NewGenerationCoordinator(local, cloud tools.CodeGenerator, val tools.Validator) *GenerationCoordinator {
	return &GenerationCoordinator{
		localGenerator:      local,
		cloudGenerator:      cloud,
		validator:           val,
		maxRetries:          3,
		escalationThreshold: 2, // Default to escalate on 3rd attempt (2 failures)
	}
}

// SetEscalationThreshold sets the number of attempts after which to switch to cloud.
func (c *GenerationCoordinator) SetEscalationThreshold(threshold int) {
	c.escalationThreshold = threshold
}

// Coordinate runs the generation loop: Generate -> Write -> Validate -> Fix.
func (c *GenerationCoordinator) Coordinate(ctx context.Context, instruction string, inputCode string, workDir string, fileName string) (*agent.Response, error) {
	// 1. Initial Generation
	fmt.Println(">> Coordinator: Requesting initial code generation (Local)...")
	generatedCode, err := c.localGenerator.Generate(ctx, instruction, inputCode)
	if err != nil {
		return nil, fmt.Errorf("initial generation failed: %w", err)
	}
	fmt.Println(">> Coordinator: Initial code generated.")

	escalated := false
	for i := 0; i <= c.maxRetries; i++ {
		// 2. Write to disk
		filePath := filepath.Join(workDir, fileName)
		err = os.WriteFile(filePath, []byte(generatedCode), 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write generated code to disk: %w", err)
		}

		// 3. Validate
		fmt.Printf(">> Coordinator: Validating (Attempt %d/%d)...\n", i+1, c.maxRetries+1)
		err = c.validator.Validate(ctx, workDir)
		if err == nil {
			// Success!
			fmt.Println(">> Coordinator: Validation PASSED.")
			return &agent.Response{
				Output: generatedCode,
				FileChanges: []agent.FileChange{
					{
						Path:    fileName,
						Content: generatedCode,
						Action:  "UPDATE",
					},
				},
				RoutingMetadata: agent.RoutingMetadata{
					Escalated: escalated,
				},
			}, nil
		}

		// 4. If failure, attempt Fix (unless we've hit retry limit)
		if i == c.maxRetries {
			return nil, fmt.Errorf("failed to generate valid code after %d retries. Last error: %w", c.maxRetries, err)
		}

		fmt.Printf(">> Coordinator: Validation FAILED: %v\n", err)

		// Determine which generator to use for Fix
		currentGenerator := c.localGenerator
		// If current attempt (0-indexed i+1) >= escalationThreshold, switch to cloud.
		// e.g. Threshold = 2. 
		// i=0 (Attempt 1): Fails. i+1 (1) < 2. Next Fix is Local.
		// i=1 (Attempt 2): Fails. i+1 (2) >= 2. Next Fix is Cloud.
		if i+1 >= c.escalationThreshold && c.cloudGenerator != nil {
			fmt.Println(">> Coordinator: Escalation threshold reached. Switching to Cloud Generator...")
			currentGenerator = c.cloudGenerator
			escalated = true
		}

		fmt.Println(">> Coordinator: Requesting FIX from agent...")
		fixedCode, fixErr := currentGenerator.Fix(ctx, generatedCode, err.Error())
		if fixErr != nil {
			return nil, fmt.Errorf("fix attempt failed: %w (original error: %v)", fixErr, err)
		}
		generatedCode = fixedCode
		fmt.Println(">> Coordinator: Agent returned fixed code.")
	}

	return &agent.Response{Output: generatedCode}, nil
}
