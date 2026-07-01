package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// TestWatchdogRender_Challenge asserts that a TypeWatchdog/challenge StreamMsg
// produces a scrollback entry whose stripped text contains "⚡ watchdog", the
// protocol, and the summary text.
func TestWatchdogRender_Challenge(t *testing.T) {
	sm := agentclient.StreamMsg{
		Type:         agentclient.TypeWatchdog,
		WatchdogKind: "challenge",
		Protocol:     "commit-checkpoint",
		Summary:      "commit the auth change before the parser work",
	}
	ev := streamMsgToEvent(sm)
	wdm, ok := ev.(watchdogEventMsg)
	if !ok {
		t.Fatalf("streamMsgToEvent(TypeWatchdog) = %T, want watchdogEventMsg", ev)
	}

	c := newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), "", "", 80, 20)
	c.Apply(wdm)
	if len(c.entries) == 0 {
		t.Fatal("Apply(watchdogEventMsg/challenge) appended no entries")
	}

	var rendered string
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	_ = md
	for _, e := range c.entries {
		rendered += stripAnsiCSI(c.renderEntry(e, 0))
	}

	if !strings.Contains(rendered, "⚡ watchdog") {
		t.Errorf("challenge render missing ⚡ watchdog, got: %q", rendered)
	}
	if !strings.Contains(rendered, "commit-checkpoint") {
		t.Errorf("challenge render missing protocol, got: %q", rendered)
	}
	if !strings.Contains(rendered, "commit the auth change before the parser work") {
		t.Errorf("challenge render missing summary text, got: %q", rendered)
	}
}

// TestWatchdogRender_Block asserts that a TypeWatchdog/block StreamMsg produces
// an entry containing "⚡ watchdog", the protocol, "(blocked — no override)", and
// the summary text.
func TestWatchdogRender_Block(t *testing.T) {
	sm := agentclient.StreamMsg{
		Type:         agentclient.TypeWatchdog,
		WatchdogKind: "block",
		Protocol:     "commit-checkpoint",
		Summary:      "commit the auth change before the parser work",
	}
	ev := streamMsgToEvent(sm)
	wdm, ok := ev.(watchdogEventMsg)
	if !ok {
		t.Fatalf("streamMsgToEvent(TypeWatchdog/block) = %T, want watchdogEventMsg", ev)
	}

	c := newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), "", "", 80, 20)
	c.Apply(wdm)
	if len(c.entries) == 0 {
		t.Fatal("Apply(watchdogEventMsg/block) appended no entries")
	}

	var rendered string
	for _, e := range c.entries {
		rendered += stripAnsiCSI(c.renderEntry(e, 0))
	}

	if !strings.Contains(rendered, "⚡ watchdog") {
		t.Errorf("block render missing ⚡ watchdog, got: %q", rendered)
	}
	if !strings.Contains(rendered, "commit-checkpoint") {
		t.Errorf("block render missing protocol, got: %q", rendered)
	}
	if !strings.Contains(rendered, "blocked") {
		t.Errorf("block render missing 'blocked', got: %q", rendered)
	}
}

// TestWatchdogRender_Echo asserts that a TypeWatchdog/echo StreamMsg produces a
// single dim line containing "<thread>:" and the summary text.
func TestWatchdogRender_Echo(t *testing.T) {
	sm := agentclient.StreamMsg{
		Type:         agentclient.TypeWatchdog,
		WatchdogKind: "echo",
		Thread:       "watchdog",
		Summary:      "boundary shift",
	}
	ev := streamMsgToEvent(sm)
	wdm, ok := ev.(watchdogEventMsg)
	if !ok {
		t.Fatalf("streamMsgToEvent(TypeWatchdog/echo) = %T, want watchdogEventMsg", ev)
	}

	c := newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), "", "", 80, 20)
	c.Apply(wdm)
	if len(c.entries) != 1 {
		t.Fatalf("Apply(watchdogEventMsg/echo) should append exactly 1 entry, got %d", len(c.entries))
	}

	rendered := stripAnsiCSI(c.renderEntry(c.entries[0], 0))

	if !strings.Contains(rendered, "watchdog:") {
		t.Errorf("echo render missing 'watchdog:', got: %q", rendered)
	}
	if !strings.Contains(rendered, "boundary shift") {
		t.Errorf("echo render missing summary, got: %q", rendered)
	}
}

// TestStreamMsgToEvent_Watchdog asserts that TypeWatchdog maps to watchdogEventMsg.
func TestStreamMsgToEvent_Watchdog(t *testing.T) {
	sm := agentclient.StreamMsg{
		Type:         agentclient.TypeWatchdog,
		WatchdogKind: "challenge",
		Protocol:     "commit-checkpoint",
		Summary:      "do the thing",
		Thread:       "main",
	}
	ev := streamMsgToEvent(sm)
	wdm, ok := ev.(watchdogEventMsg)
	if !ok {
		t.Fatalf("got %T, want watchdogEventMsg", ev)
	}
	if wdm.kind != "challenge" {
		t.Errorf("kind = %q, want %q", wdm.kind, "challenge")
	}
	if wdm.protocol != "commit-checkpoint" {
		t.Errorf("protocol = %q, want %q", wdm.protocol, "commit-checkpoint")
	}
	if wdm.summary != "do the thing" {
		t.Errorf("summary = %q, want %q", wdm.summary, "do the thing")
	}
	if wdm.thread != "main" {
		t.Errorf("thread = %q, want %q", wdm.thread, "main")
	}
}
