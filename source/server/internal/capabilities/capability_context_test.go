package capabilities

import "testing"

// alwaysContextAware is a minimal type implementing the ContextAware interface.
type alwaysContextAware struct{}

func (alwaysContextAware) WantsProjectContext() bool { return true }

// TestContextAwareInterface is a compile-time and runtime proof that the
// ContextAware interface exists with the correct method signature.
func TestContextAwareInterface(t *testing.T) {
	var ca ContextAware = alwaysContextAware{}
	if !ca.WantsProjectContext() {
		t.Fatal("WantsProjectContext() returned false, want true")
	}
}
