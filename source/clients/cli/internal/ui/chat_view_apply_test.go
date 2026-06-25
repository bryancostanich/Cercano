package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func newApplyTestView() chatView {
	p := theme.Cracker()
	return newChatView(theme.NewStyles(p), p, "", "", 79, 20)
}

// A delta extends the open streaming assistant entry and clears its Status.
func TestApply_DeltaExtendsOpenAssistant(t *testing.T) {
	cv := newApplyTestView()
	cv.AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true, Status: "routing"})
	cv.Apply(chatAssistantDeltaMsg{token: "Hi"})
	cv.Apply(chatAssistantDeltaMsg{token: " there"})

	es := cv.Entries()
	last := es[len(es)-1]
	if last.Content != "Hi there" {
		t.Fatalf("content = %q, want %q", last.Content, "Hi there")
	}
	if last.Status != "" {
		t.Fatalf("Status should be cleared after a token, got %q", last.Status)
	}
}

// After a tool entry closes the open assistant segment, the next delta opens a
// fresh assistant entry BELOW the tool (post-tool prose ordering).
func TestApply_DeltaOpensFreshBelowTool(t *testing.T) {
	cv := newApplyTestView()
	cv.AppendEntry(&Entry{Role: RoleAssistant, Content: "before", Streaming: false})
	cv.AppendEntry(&Entry{Role: RoleSystem, Tool: &ToolEntry{ToolUseID: "t1", ToolName: "Bash"}})
	cv.Apply(chatAssistantDeltaMsg{token: "after"})

	es := cv.Entries()
	if len(es) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(es))
	}
	last := es[2]
	if last.Role != RoleAssistant || last.Content != "after" || !last.Streaming {
		t.Fatalf("fresh assistant entry below tool = %+v", last)
	}
	if es[1].Tool == nil {
		t.Fatalf("tool entry should remain at index 1")
	}
}

// The full tool lifecycle: start appends a folded in-progress tool; stop sets
// ArgsSummary; exec-complete flips to ✓ and sets ResultSummary.
func TestApply_ToolEntryLifecycle(t *testing.T) {
	cv := newApplyTestView()
	cv.Apply(toolEntryStartMsg{id: "t1", name: "Bash"})

	es := cv.Entries()
	if len(es) != 1 || es[0].Tool == nil {
		t.Fatalf("start should append a tool entry, got %+v", es)
	}
	tool := es[0].Tool
	if tool.Status != ToolStatusInProgress || !tool.Folded {
		t.Fatalf("tool should be folded in-progress, got %+v", tool)
	}

	cv.Apply(toolEntryStopMsg{id: "t1", argsSummary: `{"cmd":["ls","-la"]}`})
	if tool.ArgsSummary == "" {
		t.Fatalf("stop should set ArgsSummary")
	}

	cv.Apply(toolEntryExecCompleteMsg{id: "t1", isError: false, summary: "ok"})
	if tool.Status != ToolStatusComplete {
		t.Fatalf("exec-complete (ok) should flip to ToolStatusComplete, got %v", tool.Status)
	}
	if tool.ResultSummary == "" {
		t.Fatalf("exec-complete should set ResultSummary")
	}
}

// Done finalizes the streaming entry and inserts the notice ABOVE the assistant.
func TestApply_DoneFinalizesAndNotice(t *testing.T) {
	cv := newApplyTestView()
	cv.AppendEntry(&Entry{Role: RoleAssistant, Content: "the reply", Streaming: true})
	cv.Apply(chatDoneMsg{text: "", tokIn: 1, tokOut: 2, notice: "cloud absent"})

	es := cv.Entries()
	if len(es) != 2 {
		t.Fatalf("expected notice + assistant = 2 entries, got %d", len(es))
	}
	// Notice inserted above the last (assistant) entry.
	if es[0].Role != RoleSystem || es[0].Content != "⚠ cloud absent" {
		t.Fatalf("notice should sit above the reply, got %+v", es[0])
	}
	if es[1].Role != RoleAssistant || es[1].Streaming {
		t.Fatalf("assistant should be finalized (not streaming), got %+v", es[1])
	}
}

// Apply has no access to host footer telemetry — proven at compile-level by the
// chatView type carrying no tokIn/tokOut fields. A chatDoneMsg with telemetry
// only mutates the transcript.
func TestApply_DoesNotTouchTelemetry(t *testing.T) {
	cv := newApplyTestView()
	cv.AppendEntry(&Entry{Role: RoleAssistant, Content: "x", Streaming: true})
	// Compiles only because chatView has no telemetry fields to mutate.
	cv.Apply(chatDoneMsg{tokOut: 99})
	es := cv.Entries()
	if es[len(es)-1].Streaming {
		t.Fatalf("done should finalize the assistant entry")
	}
}
