package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

// TestChatViewNavCyclesToolEntries verifies that EnterToolNav focuses the last
// tool entry, NavPrev/NavNext cycle through tool entries, ToggleFocusedFold
// flips Folded on the focused entry, and ExitToolNav clears focus.
func TestChatViewNavCyclesToolEntries(t *testing.T) {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", 80, 20)

	// entries: user(0), tool(1), asst(2), tool(3)
	cv.AppendEntry(&Entry{Role: RoleUser, Content: "hi"})
	cv.AppendEntry(&Entry{Role: RoleAssistant, Tool: &ToolEntry{
		ToolName: "Bash",
		Status:   ToolStatusComplete,
		Folded:   false,
	}})
	cv.AppendEntry(&Entry{Role: RoleAssistant, Content: "ok"})
	cv.AppendEntry(&Entry{Role: RoleAssistant, Tool: &ToolEntry{
		ToolName: "Read",
		Status:   ToolStatusComplete,
		Folded:   false,
	}})

	// EnterToolNav should focus the LAST tool entry (index 3).
	if !cv.EnterToolNav() {
		t.Fatal("EnterToolNav returned false; expected true when tool entries exist")
	}
	if !cv.InToolNav() {
		t.Fatal("InToolNav should be true after EnterToolNav")
	}
	if cv.focusedToolIdx != 3 {
		t.Fatalf("focusedToolIdx = %d, want 3 (last tool entry)", cv.focusedToolIdx)
	}

	// NavPrev should move to the previous tool entry (index 1).
	cv.NavPrev()
	if cv.focusedToolIdx != 1 {
		t.Fatalf("after NavPrev: focusedToolIdx = %d, want 1", cv.focusedToolIdx)
	}

	// NavPrev at first tool entry is a no-op (clamped).
	cv.NavPrev()
	if cv.focusedToolIdx != 1 {
		t.Fatalf("NavPrev at first entry: focusedToolIdx = %d, want 1 (clamped)", cv.focusedToolIdx)
	}

	// NavNext should return to index 3.
	cv.NavNext()
	if cv.focusedToolIdx != 3 {
		t.Fatalf("after NavNext: focusedToolIdx = %d, want 3", cv.focusedToolIdx)
	}

	// NavNext at last tool entry is a no-op (clamped).
	cv.NavNext()
	if cv.focusedToolIdx != 3 {
		t.Fatalf("NavNext at last entry: focusedToolIdx = %d, want 3 (clamped)", cv.focusedToolIdx)
	}

	// ToggleFocusedFold on entry 3 should flip Folded to true.
	cv.ToggleFocusedFold()
	if !cv.entries[3].Tool.Folded {
		t.Fatal("ToggleFocusedFold should have set Folded=true on entry 3")
	}

	// Toggle again should flip back to false.
	cv.ToggleFocusedFold()
	if cv.entries[3].Tool.Folded {
		t.Fatal("second ToggleFocusedFold should have set Folded=false on entry 3")
	}

	// ExitToolNav clears focus.
	cv.ExitToolNav()
	if cv.InToolNav() {
		t.Fatal("InToolNav should be false after ExitToolNav")
	}
	if cv.focusedToolIdx != -1 {
		t.Fatalf("focusedToolIdx = %d, want -1 after ExitToolNav", cv.focusedToolIdx)
	}
}

// TestChatViewEnterNavNoToolsNoop verifies that EnterToolNav returns false and
// leaves focus at -1 when there are no tool entries.
func TestChatViewEnterNavNoToolsNoop(t *testing.T) {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", 80, 20)

	cv.AppendEntry(&Entry{Role: RoleUser, Content: "hello"})
	cv.AppendEntry(&Entry{Role: RoleAssistant, Content: "world"})

	if cv.EnterToolNav() {
		t.Fatal("EnterToolNav should return false when no tool entries exist")
	}
	if cv.InToolNav() {
		t.Fatal("InToolNav should be false when EnterToolNav returned false")
	}
	if cv.focusedToolIdx != -1 {
		t.Fatalf("focusedToolIdx = %d, want -1 when no tools", cv.focusedToolIdx)
	}
}
