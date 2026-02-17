package agent_test

import (
	"testing"

	"cercano/source/server/internal/agent"
)

func TestInterfaces(t *testing.T) {
	// Verify UnitTestHandler implements CodeGenerator
	var _ agent.CodeGenerator = (*agent.UnitTestHandler)(nil)
}
