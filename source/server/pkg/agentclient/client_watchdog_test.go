package agentclient

import (
	"testing"

	"cercano/source/server/pkg/proto"
)

// TestWatchdogEventChallengeMapsToStreamMsg verifies that a WatchdogEvent
// proto with Kind="challenge" is converted to a TypeWatchdog StreamMsg with
// the correct field mapping: Protocol from proto.Protocol, Summary from proto.Text.
func TestWatchdogEventChallengeMapsToStreamMsg(t *testing.T) {
	we := &proto.WatchdogEvent{
		Kind:     "challenge",
		Protocol: "commit-checkpoint",
		Text:     "x",
	}

	msg := streamMsgFromWatchdogEvent(we)

	if msg.Type != TypeWatchdog {
		t.Errorf("Type: got %v, want TypeWatchdog", msg.Type)
	}
	if msg.WatchdogKind != "challenge" {
		t.Errorf("WatchdogKind: got %q, want %q", msg.WatchdogKind, "challenge")
	}
	if msg.Protocol != "commit-checkpoint" {
		t.Errorf("Protocol: got %q, want %q", msg.Protocol, "commit-checkpoint")
	}
	if msg.Summary != "x" {
		t.Errorf("Summary: got %q, want %q", msg.Summary, "x")
	}
	if msg.Thread != "" {
		t.Errorf("Thread: got %q, want empty", msg.Thread)
	}
}

// TestWatchdogEventBlockMapsToStreamMsg verifies that Kind="block" is preserved.
func TestWatchdogEventBlockMapsToStreamMsg(t *testing.T) {
	we := &proto.WatchdogEvent{
		Kind:     "block",
		Protocol: "commit-checkpoint",
		Text:     "blocked: commit first",
	}

	msg := streamMsgFromWatchdogEvent(we)

	if msg.WatchdogKind != "block" {
		t.Errorf("WatchdogKind: got %q, want %q", msg.WatchdogKind, "block")
	}
	if msg.Protocol != "commit-checkpoint" {
		t.Errorf("Protocol: got %q, want %q", msg.Protocol, "commit-checkpoint")
	}
}

// TestWatchdogEventEchoMapsToStreamMsg verifies that Kind="echo" maps
// Thread from proto.Thread and Summary from proto.Text.
func TestWatchdogEventEchoMapsToStreamMsg(t *testing.T) {
	we := &proto.WatchdogEvent{
		Kind:   "echo",
		Text:   "boundary shift",
		Thread: "watchdog",
	}

	msg := streamMsgFromWatchdogEvent(we)

	if msg.WatchdogKind != "echo" {
		t.Errorf("WatchdogKind: got %q, want %q", msg.WatchdogKind, "echo")
	}
	if msg.Summary != "boundary shift" {
		t.Errorf("Summary: got %q, want %q", msg.Summary, "boundary shift")
	}
	if msg.Thread != "watchdog" {
		t.Errorf("Thread: got %q, want %q", msg.Thread, "watchdog")
	}
	if msg.Protocol != "" {
		t.Errorf("Protocol: got %q, want empty for echo", msg.Protocol)
	}
}
