package tools_test

import (
	"testing"

	"cercano/source/server/internal/tools"
)

func TestInterfaces(t *testing.T) {
	// Verify UnitTestHandler implements CodeGenerator
	var _ tools.CodeGenerator = (*tools.UnitTestHandler)(nil)
}