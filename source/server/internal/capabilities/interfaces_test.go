package capabilities_test

import (
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestInterfaces(t *testing.T) {
	// Verify UnitTestHandler implements CodeGenerator
	var _ capabilities.CodeGenerator = (*capabilities.UnitTestHandler)(nil)
}