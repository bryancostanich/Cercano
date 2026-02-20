package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
func (c *GenerationCoordinator) Coordinate(ctx context.Context, instruction string, inputCode string, workDir string, fileName string, progress agent.ProgressFunc) (*agent.Response, error) {
	if progress == nil {
		progress = func(string) {}
	}

	// 0. Infer Target Filename
	// Ask the generator what file we should be modifying/creating based on the instruction
	progress("Planning: Identifying target file...")
	targetFilePrompt := fmt.Sprintf("Based on the instruction '%s' and the current file '%s', what is the single filename that should be modified or created? Return ONLY the filename.", instruction, fileName)
	inferredName, err := c.localGenerator.Generate(ctx, targetFilePrompt, "")
	if err == nil {
		inferredName = strings.TrimSpace(inferredName)
		// Basic sanity check: ensure it looks like a filename
		if inferredName != "" && !strings.Contains(inferredName, " ") && strings.Contains(inferredName, ".") {
			if inferredName != fileName {
				fmt.Printf(">> Coordinator: Inferred target file '%s' (was '%s')\n", inferredName, fileName)
				fileName = inferredName
			}
		}
	}

	targetPath := filepath.Join(workDir, fileName)
	var backupContent []byte
	backupExists := false

	// Create backup of original file if it exists
	if _, err := os.Stat(targetPath); err == nil {
		content, readErr := os.ReadFile(targetPath)
		if readErr == nil {
			backupContent = content
			backupExists = true
			fmt.Printf(">> Coordinator: Created backup of %s\n", fileName)
		}
	}

	// Helper to restore backup on failure or cleanup
	restore := func() {
		if backupExists {
			os.WriteFile(targetPath, backupContent, 0644)
			fmt.Printf(">> Coordinator: Cleanup - Restored original %s\n", fileName)
		} else {
			os.Remove(targetPath)
			fmt.Printf(">> Coordinator: Cleanup - Removed temporary %s\n", fileName)
		}
	}

	// 1. Initial Generation
	fmt.Println(">> Coordinator: Requesting initial code generation (Local)...")
	generatedCode, err := c.localGenerator.Generate(ctx, instruction, inputCode)
	if err != nil {
		restore()
		progress("Generating code... Failed.")
		return nil, fmt.Errorf("initial generation failed: %w", err)
	}
	progress("Generating code... Done.")
	fmt.Println(">> Coordinator: Initial code generated.")

	escalated := false
	var lastValidationError string

	for i := 0; i <= c.maxRetries; i++ {
		// 2. Write to disk (CLEANED)
		cleanCode := tools.CleanMarkdown(generatedCode)
		err = os.WriteFile(targetPath, []byte(cleanCode), 0644)
		if err != nil {
			restore()
			return nil, fmt.Errorf("failed to write generated code to disk: %w", err)
		}

		// 3. Validate
		msg := fmt.Sprintf("Validating (Attempt %d/%d)...", i+1, c.maxRetries+1)
		progress(msg)
		fmt.Printf(">> Coordinator: %s\n", msg)
		err = c.validator.Validate(ctx, workDir)
		if err == nil {
			// Success!
			fmt.Println(">> Coordinator: Validation PASSED.")
			progress(msg + " Success.")
			
			// CLEANUP: Restore the original state before returning.
			// This ensures the workspace stays clean and the user MUST click "Apply" in the IDE.
			restore()

			// Format the output for chat nicely
			chatOutput := generatedCode
			if !strings.Contains(chatOutput, "```") {
				chatOutput = fmt.Sprintf("I've generated the code for **%s**:\n\n```go\n%s\n```", fileName, generatedCode)
			}
			
			return &agent.Response{
				Output: chatOutput,
				FileChanges: []agent.FileChange{
					{
						Path:    fileName,
						Content: cleanCode,
						Action:  "UPDATE",
					},
				},
				RoutingMetadata: agent.RoutingMetadata{
					Escalated: escalated,
				},
				ValidationErrors: lastValidationError,
			}, nil
		}

		// 4. If failure, attempt Fix (unless we've hit retry limit)
		lastValidationError = err.Error()
		if i == c.maxRetries {
			restore()
			progress(msg + " Failed.")
			return &agent.Response{
				Output:           fmt.Sprintf("Failed to generate valid code after %d attempts.", c.maxRetries+1),
				ValidationErrors: lastValidationError,
			}, nil
		}

		fmt.Printf(">> Coordinator: Validation FAILED: %v\n", err)
		progress(msg + " Failed. Retrying.")

		// Determine which generator to use for Fix
		currentGenerator := c.localGenerator
		if i+1 >= c.escalationThreshold && c.cloudGenerator != nil {
			escalationMsg := "Escalating to Cloud for self-correction..."
			progress(escalationMsg)
			fmt.Println(">> Coordinator: " + escalationMsg)
			currentGenerator = c.cloudGenerator
			escalated = true
		} else {
			progress(fmt.Sprintf("Self-Correction (Attempt %d/%d)...", i+1, c.maxRetries))
		}

		fmt.Println(">> Coordinator: Requesting FIX from agent...")
		fixedCode, fixErr := currentGenerator.Fix(ctx, generatedCode, err.Error())
		if fixErr != nil {
			restore()
			return nil, fmt.Errorf("fix attempt failed: %w (original error: %v)", fixErr, err)
		}
		generatedCode = fixedCode
		fmt.Println(">> Coordinator: Agent returned fixed code.")
	}

	return &agent.Response{Output: generatedCode}, nil
}
