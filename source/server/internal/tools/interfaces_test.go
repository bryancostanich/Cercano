package tools_test

import (
	"testing"

	"cercano/source/server/internal/tools"
)

func TestCodeGenerator_Generate_Signature(t *testing.T) {
	// Just verify compilation of the signature
	var gen tools.CodeGenerator = (*tools.UnitTestHandler)(nil)
	_ = gen
}

func TestInterfaces(t *testing.T) {
	// Verify UnitTestHandler implements CodeGenerator
	var _ tools.CodeGenerator = (*tools.UnitTestHandler)(nil)
}
