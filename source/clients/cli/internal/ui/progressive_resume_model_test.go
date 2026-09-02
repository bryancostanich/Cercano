package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/server/pkg/agentclient"

	"github.com/charmbracelet/x/ansi"
)

func TestModelProgressiveResumeTailOlderAndCompletion(t *testing.T) {
	m := New(nil, false)
	m.resumeGen = 7
	m.resumeHydrating = true
	m.resumeBackfilling = true

	next, cmd := m.applyProgressiveResumeEvent(resumeViewportStreamMsg{gen: 7, event: agentclient.ResumeViewportEvent{
		Kind:       agentclient.ResumeViewportEventTail,
		StartIndex: 2,
		TotalTurns: 4,
		Turns: []agentclient.PersistedTurn{
			{Role: "assistant", Content: "tail assistant", CreatedAt: time.Unix(3, 0)},
		},
	}})
	m = next
	if cmd != nil {
		t.Fatalf("tail event with nil stream channel returned unexpected command")
	}
	content := ansi.Strip(chatLayoutContent(m.mainChat()))
	if !strings.Contains(content, "tail assistant") || !strings.Contains(content, progressiveOlderLoadingText) {
		t.Fatalf("tail event content = %q", content)
	}

	next, _ = m.applyProgressiveResumeEvent(resumeViewportStreamMsg{gen: 7, event: agentclient.ResumeViewportEvent{
		Kind:       agentclient.ResumeViewportEventOlder,
		StartIndex: 0,
		TotalTurns: 4,
		Turns: []agentclient.PersistedTurn{
			{Role: "user", Content: "older prompt", CreatedAt: time.Unix(1, 0)},
		},
	}})
	m = next
	content = ansi.Strip(chatLayoutContent(m.mainChat()))
	if strings.Index(content, "older prompt") > strings.Index(content, "tail assistant") {
		t.Fatalf("older chunk not before tail: %q", content)
	}

	next, _ = m.applyProgressiveResumeEvent(resumeViewportStreamMsg{gen: 7, event: agentclient.ResumeViewportEvent{Kind: agentclient.ResumeViewportEventBackfillComplete}})
	m = next
	content = ansi.Strip(chatLayoutContent(m.mainChat()))
	if m.resumeBackfilling {
		t.Fatalf("backfill flag still set")
	}
	if strings.Contains(content, progressiveOlderLoadingText) {
		t.Fatalf("sentinel still visible after backfill complete: %q", content)
	}
	if len(m.inputHistory) != 1 || m.inputHistory[0] != "older prompt" {
		t.Fatalf("input history = %#v, want older prompt", m.inputHistory)
	}
}

func TestSubmitBlockedWhileProgressiveResumeHydrating(t *testing.T) {
	m := New(nil, false)
	m.resumeHydrating = true
	next, cmd := m.submit("hello", nil)
	if cmd != nil {
		t.Fatalf("submit while hydrating returned command")
	}
	got := next.(Model)
	if got.streaming {
		t.Fatalf("submit while hydrating started streaming")
	}
	if len(got.inputHistory) != 0 {
		t.Fatalf("submit while hydrating recorded prompt history: %#v", got.inputHistory)
	}
	if !strings.Contains(got.errMsg, "rehydrating") {
		t.Fatalf("errMsg = %q, want rehydrating hint", got.errMsg)
	}
}
