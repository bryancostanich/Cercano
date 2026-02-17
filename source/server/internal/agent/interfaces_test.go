package agent_test

import (
	"testing"

	"cercano/source/internal/agent"
)

func TestInterfaces(t *testing.T) {
	// Verify UnitTestHandler implements CodeGenerator
	var _ agent.CodeGenerator = (*agent.UnitTestHandler)(nil)
}
