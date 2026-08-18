package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{900 * time.Millisecond, "0s"},
		{4200 * time.Millisecond, "4s"},
		{59*time.Second + 900*time.Millisecond, "59s"},
		{60 * time.Second, "1m00s"},
		{65 * time.Second, "1m05s"},
		{334 * time.Second, "5m34s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestToolProgressActivity(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		started int
		done    int
		want    string
	}{
		{"first tool", "Bash", 1, 0, "running Bash (tool 1, 0 done)"},
		{"third tool", "Read", 3, 2, "running Read (tool 3, 2 done)"},
		{"missing tool", "", 1, 0, "running tool (tool 1, 0 done)"},
		{"no counter", "Bash", 0, 0, "running Bash"},
	}
	for _, c := range cases {
		if got := toolProgressActivity(c.tool, c.started, c.done); got != c.want {
			t.Errorf("%s: toolProgressActivity = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestModelToolProgressCountersUpdateAndReset(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.turnStart = time.Unix(10, 0)

	next, _ := m.Update(chatStreamMsg{gen: m.turnGen, ev: toolEntryStartMsg{id: "t1", name: "Bash"}})
	m = next.(Model)
	if m.turnToolStarted != 1 || m.turnToolDone != 0 || m.turnActivity != "running Bash (tool 1, 0 done)" {
		t.Fatalf("after first start: started=%d done=%d activity=%q", m.turnToolStarted, m.turnToolDone, m.turnActivity)
	}

	next, _ = m.Update(chatStreamMsg{gen: m.turnGen, ev: toolEntryStartMsg{id: "t2", name: "Read"}})
	m = next.(Model)
	if m.turnToolStarted != 2 || m.turnToolDone != 0 || m.turnActivity != "running Read (tool 2, 0 done)" {
		t.Fatalf("after second start: started=%d done=%d activity=%q", m.turnToolStarted, m.turnToolDone, m.turnActivity)
	}

	next, _ = m.Update(chatStreamMsg{gen: m.turnGen, ev: toolEntryExecCompleteMsg{id: "t1", summary: "ok"}})
	m = next.(Model)
	if m.turnToolStarted != 2 || m.turnToolDone != 1 || m.turnActivity != "completed 1/2 tools" {
		t.Fatalf("after complete: started=%d done=%d activity=%q", m.turnToolStarted, m.turnToolDone, m.turnActivity)
	}

	next, _ = m.Update(chatStreamMsg{gen: m.turnGen, ev: chatDoneMsg{text: "done"}})
	m = next.(Model)
	if m.turnToolStarted != 0 || m.turnToolDone != 0 {
		t.Fatalf("chat done should reset tool counters, started=%d done=%d", m.turnToolStarted, m.turnToolDone)
	}
}

func TestTurnStatusLine(t *testing.T) {
	cases := []struct {
		name, activity string
		elapsed        time.Duration
		tokOut         int
		model          string
		isCloud        bool
		want           string
	}{
		{"thinking local no tokens", "thinking", time.Second, 0, "qwen3-coder", false,
			"thinking · 1s · qwen3-coder (local)"},
		{"running tool cloud", "running Bash", 4 * time.Second, 0, "claude-opus", true,
			"running Bash · 4s · claude-opus (cloud)"},
		{"writing with tokens", "writing", 4 * time.Second, 312, "qwen3-coder", false,
			"writing · 4s · 312 tok↑ · qwen3-coder (local)"},
		{"no engine yet", "thinking", time.Second, 0, "", false,
			"thinking · 1s"},
	}
	for _, c := range cases {
		if got := turnStatusLine(c.activity, c.elapsed, c.tokOut, c.model, c.isCloud); got != c.want {
			t.Errorf("%s: turnStatusLine = %q, want %q", c.name, got, c.want)
		}
	}
}
