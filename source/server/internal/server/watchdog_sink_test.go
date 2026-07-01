package server

import (
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/pkg/proto"
)

// mockSinkStream captures Send calls for sink unit tests.
type mockSinkStream struct {
	sent []*proto.StreamProcessResponse
}

func (m *mockSinkStream) Send(r *proto.StreamProcessResponse) error {
	m.sent = append(m.sent, r)
	return nil
}

// buildTestSink constructs a sink func wired to the given mock stream,
// mirroring the inline closure in server.go's StreamProcess.
func buildTestSink(ms *mockSinkStream) func(agent.LoopEvent) {
	return func(ev agent.LoopEvent) {
		switch ev.Kind {
		case agent.LoopWatchdogChallenge, agent.LoopWatchdogBlock:
			kind := "challenge"
			if ev.Kind == agent.LoopWatchdogBlock {
				kind = "block"
			}
			ms.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: kind, Protocol: ev.Detail, Text: ev.Summary,
					},
				},
			})
		case agent.LoopWatchdogEcho:
			ms.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: "echo", Text: ev.Summary, Thread: ev.ToolName,
					},
				},
			})
		}
	}
}

// TestSinkMapsWatchdogChallenge verifies challenge LoopEvents produce a
// WatchdogEvent proto with Kind=="challenge", Protocol from Detail, Text from Summary.
func TestSinkMapsWatchdogChallenge(t *testing.T) {
	ms := &mockSinkStream{}
	sink := buildTestSink(ms)

	sink(agent.LoopEvent{
		Kind:    agent.LoopWatchdogChallenge,
		Detail:  "commit-checkpoint",
		Summary: "commit first",
	})

	if len(ms.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(ms.sent))
	}
	we := ms.sent[0].GetWatchdogEvent()
	if we == nil {
		t.Fatal("expected WatchdogEvent payload, got nil")
	}
	if we.Kind != "challenge" {
		t.Errorf("Kind: got %q, want %q", we.Kind, "challenge")
	}
	if we.Protocol != "commit-checkpoint" {
		t.Errorf("Protocol: got %q, want %q", we.Protocol, "commit-checkpoint")
	}
	if we.Text != "commit first" {
		t.Errorf("Text: got %q, want %q", we.Text, "commit first")
	}
}

// TestSinkMapsWatchdogBlock verifies block LoopEvents produce a
// WatchdogEvent proto with Kind=="block".
func TestSinkMapsWatchdogBlock(t *testing.T) {
	ms := &mockSinkStream{}
	sink := buildTestSink(ms)

	sink(agent.LoopEvent{
		Kind:    agent.LoopWatchdogBlock,
		Detail:  "commit-checkpoint",
		Summary: "blocked: commit first",
	})

	if len(ms.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(ms.sent))
	}
	we := ms.sent[0].GetWatchdogEvent()
	if we == nil {
		t.Fatal("expected WatchdogEvent payload, got nil")
	}
	if we.Kind != "block" {
		t.Errorf("Kind: got %q, want %q", we.Kind, "block")
	}
	if we.Protocol != "commit-checkpoint" {
		t.Errorf("Protocol: got %q, want %q", we.Protocol, "commit-checkpoint")
	}
}

// TestSinkMapsWatchdogEcho verifies echo LoopEvents produce a
// WatchdogEvent proto with Kind=="echo", Thread from ToolName, Text from Summary.
func TestSinkMapsWatchdogEcho(t *testing.T) {
	ms := &mockSinkStream{}
	sink := buildTestSink(ms)

	sink(agent.LoopEvent{
		Kind:     agent.LoopWatchdogEcho,
		ToolName: "watchdog",
		Summary:  "boundary shift",
	})

	if len(ms.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(ms.sent))
	}
	we := ms.sent[0].GetWatchdogEvent()
	if we == nil {
		t.Fatal("expected WatchdogEvent payload, got nil")
	}
	if we.Kind != "echo" {
		t.Errorf("Kind: got %q, want %q", we.Kind, "echo")
	}
	if we.Thread != "watchdog" {
		t.Errorf("Thread: got %q, want %q", we.Thread, "watchdog")
	}
	if we.Text != "boundary shift" {
		t.Errorf("Text: got %q, want %q", we.Text, "boundary shift")
	}
}
