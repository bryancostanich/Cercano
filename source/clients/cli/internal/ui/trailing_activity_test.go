package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

// IsBetweenPhases is false when nothing is streaming — the whole indicator
// only makes sense as a "still working" signal mid-turn.
func TestChatView_IsBetweenPhases_FalseWhenNotStreaming(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetEntriesSlice([]*Entry{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "done"},
	})
	if c.IsBetweenPhases() {
		t.Errorf("not streaming: IsBetweenPhases must be false")
	}
}

// While a tool is in-progress, the tool spinner already covers liveness;
// trailing activity should be silent to avoid two competing indicators.
func TestChatView_IsBetweenPhases_FalseWhileToolInProgress(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(true)
	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{ToolName: "Read", Status: ToolStatusInProgress, StartedAt: time.Now()}},
	})
	if c.IsBetweenPhases() {
		t.Errorf("tool in progress: IsBetweenPhases must be false")
	}
}

// The empty-content streaming assistant is its own (pre-text placeholder)
// indicator. Trailing activity should defer to it.
func TestChatView_IsBetweenPhases_FalseWhileStreamingTextEntry(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(true)
	c.SetEntriesSlice([]*Entry{
		{Role: RoleAssistant, Content: "", Streaming: true},
	})
	if c.IsBetweenPhases() {
		t.Errorf("streaming empty assistant: IsBetweenPhases must be false (placeholder covers it)")
	}
}

// A streaming text entry that is still actively receiving tokens (last token
// within the stale threshold) is a live stream — the prose itself is the
// visible activity, so the trailing line must stay silent.
func TestChatView_IsBetweenPhases_FalseWhileStreamFresh(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(true)
	c.SetEntriesSlice([]*Entry{
		{Role: RoleAssistant, Content: "Let me write the spec.", Streaming: true},
	})
	now := time.Now()
	c.SetAnimationTime(now)
	c.lastTokenAt = now.Add(-staleStreamThreshold / 2) // token just arrived
	if c.IsBetweenPhases() {
		t.Errorf("fresh stream (token within threshold): IsBetweenPhases must be false")
	}
}

// The dead-zone case this change fixes: a streaming text entry with content
// that has gone quiet past the threshold — the model finished a prose segment
// and is off doing work with nothing flowing. The trailing line must appear.
func TestChatView_IsBetweenPhases_TrueWhileStreamStale(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(true)
	c.SetEntriesSlice([]*Entry{
		{Role: RoleAssistant, Content: "Let me write the spec.", Streaming: true},
	})
	now := time.Now()
	c.SetAnimationTime(now)
	c.lastTokenAt = now.Add(-2 * staleStreamThreshold) // stalled well past threshold
	if !c.IsBetweenPhases() {
		t.Errorf("stale stream (no token past threshold): IsBetweenPhases must be true")
	}
}

// The target case: tools completed, no streaming text entry, but still
// streaming. This is the gap the trailing activity line covers.
func TestChatView_IsBetweenPhases_TrueAfterToolsComplete(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(true)
	c.SetEntriesSlice([]*Entry{
		{Role: RoleAssistant, Content: "thinking out loud"},
		{Tool: &ToolEntry{ToolName: "Read", Status: ToolStatusComplete, Duration: 5 * time.Millisecond}},
	})
	if !c.IsBetweenPhases() {
		t.Errorf("post-tool, streaming, no new entry: IsBetweenPhases must be true")
	}
}

// The trailing line renders BELOW the last entry when IsBetweenPhases.
// The line should include an indicator (spinner/activity text), and the
// scrollback output should contain something distinct from just the
// entries.
func TestChatView_TrailingActivityAppears(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(true)
	entries := []*Entry{
		{Role: RoleUser, Content: "do it"},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
	}
	c.SetEntriesSlice(entries)
	c.SetEntries(entries)
	got := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))
	// The trailing activity uses turnStatusLine which includes either
	// "thinking" (the default) or the current activity. Verify SOMETHING
	// indicating work appears AFTER the tool entry.
	if !strings.Contains(got, "thinking") {
		t.Errorf("expected 'thinking' activity in trailing line, got:\n%s", got)
	}
}

// When not streaming, the trailing line must NOT appear — it would
// misleadingly imply work is still happening.
func TestChatView_TrailingActivityHiddenWhenNotStreaming(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	c.SetStreaming(false)
	entries := []*Entry{
		{Role: RoleUser, Content: "do it"},
		{Tool: &ToolEntry{ToolName: "Read", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
	}
	c.SetEntriesSlice(entries)
	c.SetEntries(entries)
	got := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))
	if strings.Contains(got, "thinking") {
		t.Errorf("trailing 'thinking' line should not appear when not streaming, got:\n%s", got)
	}
}
