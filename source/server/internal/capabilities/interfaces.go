package capabilities

import "context"

// CodeGenerator defines the interface for generating and fixing code.
type CodeGenerator interface {
	// Generate generates code based on the input.
	Generate(ctx context.Context, code string) (string, error)
	// Fix attempts to fix the code based on the provided error message.
	Fix(ctx context.Context, code string, errorMsg string) (string, error)
}

// Validator defines the interface for validating code (e.g., running tests).
type Validator interface {
	// Validate runs validation logic (e.g., "go test") in the specified directory.
	// It returns an error if validation fails, containing the output.
	Validate(ctx context.Context, workDir string) error
}
