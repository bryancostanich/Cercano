package ui

import "testing"

// TestOpenConversationOnStart_SetsField verifies the launch-data setter arms
// an auto-resume and hides the splash, mirroring OpenWizardOnStart.
func TestOpenConversationOnStart_SetsField(t *testing.T) {
	m := New(nil, false).OpenConversationOnStart("conv-xyz")
	if m.autoResumeConvID != "conv-xyz" {
		t.Fatalf("autoResumeConvID = %q, want conv-xyz", m.autoResumeConvID)
	}
	if m.splashShown {
		t.Fatal("splash should be hidden when a resume is armed")
	}
}
