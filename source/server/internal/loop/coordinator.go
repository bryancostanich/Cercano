package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"cercano/source/server/internal/tools"
)

// GenerationCoordinator orchestrates the iterative generation and validation of code.
type GenerationCoordinator struct {
	generator tools.CodeGenerator
	validator tools.Validator
	maxRetries int
}

// NewGenerationCoordinator creates a new coordinator.
func NewGenerationCoordinator(gen tools.CodeGenerator, val tools.Validator) *GenerationCoordinator {
	return &GenerationCoordinator{
		generator: gen,
		validator: val,
		maxRetries: 3,
	}
}

// Coordinate runs the generation loop: Generate -> Write -> Validate -> Fix.
func (c *GenerationCoordinator) Coordinate(ctx context.Context, instruction string, inputCode string, workDir string, fileName string) (string, error) {
	// 1. Initial Generation
	fmt.Println(">> Coordinator: Requesting initial code generation...")
	generatedCode, err := c.generator.Generate(ctx, instruction, inputCode)
	if err != nil {
		return "", fmt.Errorf("initial generation failed: %w", err)
	}
	fmt.Println(">> Coordinator: Initial code generated.")

	for i := 0; i <= c.maxRetries; i++ {
		// 2. Write to disk
		filePath := filepath.Join(workDir, fileName)
		err = os.WriteFile(filePath, []byte(generatedCode), 0644)
		if err != nil {
			return "", fmt.Errorf("failed to write generated code to disk: %w", err)
		}

		// 3. Validate
		fmt.Printf(">> Coordinator: Validating (Attempt %d/%d)...\n", i+1, c.maxRetries+1)
		err = c.validator.Validate(ctx, workDir)
		if err == nil {
			// Success!
			fmt.Println(">> Coordinator: Validation PASSED.")
			return generatedCode, nil
		}

		// 4. If failure, attempt Fix (unless we've hit retry limit)
		if i == c.maxRetries {
			return "", fmt.Errorf("failed to generate valid code after %d retries. Last error: %w", c.maxRetries, err)
		}

		fmt.Printf(">> Coordinator: Validation FAILED: %v\n", err)
		fmt.Println(">> Coordinator: Requesting FIX from agent...")
		
		fixedCode, fixErr := c.generator.Fix(ctx, generatedCode, err.Error())
		if fixErr != nil {
			return "", fmt.Errorf("fix attempt failed: %w (original error: %v)", fixErr, err)
		}
		generatedCode = fixedCode
		fmt.Println(">> Coordinator: Agent returned fixed code.")
	}

	return generatedCode, nil
}
