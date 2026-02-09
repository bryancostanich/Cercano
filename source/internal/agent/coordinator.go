package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// GenerationCoordinator orchestrates the iterative generation and validation of code.
type GenerationCoordinator struct {
	generator CodeGenerator
	validator Validator
	maxRetries int
}

// NewGenerationCoordinator creates a new coordinator.
func NewGenerationCoordinator(gen CodeGenerator, val Validator) *GenerationCoordinator {
	return &GenerationCoordinator{
		generator: gen,
		validator: val,
		maxRetries: 3,
	}
}

// Coordinate runs the generation loop: Generate -> Write -> Validate -> Fix.
func (c *GenerationCoordinator) Coordinate(ctx context.Context, inputCode, workDir, fileName string) (string, error) {
	// 1. Initial Generation
	generatedCode, err := c.generator.Generate(ctx, inputCode)
	if err != nil {
		return "", fmt.Errorf("initial generation failed: %w", err)
	}

	for i := 0; i <= c.maxRetries; i++ {
		// 2. Write to disk
		filePath := filepath.Join(workDir, fileName)
		err = os.WriteFile(filePath, []byte(generatedCode), 0644)
		if err != nil {
			return "", fmt.Errorf("failed to write generated code to disk: %w", err)
		}

		// 3. Validate
		err = c.validator.Validate(ctx, workDir)
		if err == nil {
			// Success!
			return generatedCode, nil
		}

		// 4. If failure, attempt Fix (unless we've hit retry limit)
		if i == c.maxRetries {
			return "", fmt.Errorf("failed to generate valid code after %d retries. Last error: %w", c.maxRetries, err)
		}

		fmt.Printf("Validation failed (attempt %d): %v. Attempting fix...\n", i+1, err)
		
		fixedCode, fixErr := c.generator.Fix(ctx, generatedCode, err.Error())
		if fixErr != nil {
			return "", fmt.Errorf("fix attempt failed: %w (original error: %v)", fixErr, err)
		}
		generatedCode = fixedCode
	}

	return generatedCode, nil
}
