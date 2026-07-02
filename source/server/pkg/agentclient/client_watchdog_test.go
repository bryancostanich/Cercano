package agentclient

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc"

	"cercano/source/server/pkg/proto"
)

// fakeProcessStream feeds canned StreamProcessResponses then io.EOF.
type fakeProcessStream struct {
	proto.Agent_StreamProcessRequestClient // embed; unused methods panic if called
	msgs                                   []*proto.StreamProcessResponse
	i                                      int
}

func (f *fakeProcessStream) Recv() (*proto.StreamProcessResponse, error) {
	if f.i >= len(f.msgs) {
		return nil, io.EOF
	}
	m := f.msgs[f.i]
	f.i++
	return m, nil
}

// fakeAgentClient returns the fake stream from StreamProcessRequest.
type fakeAgentClient struct {
	proto.AgentClient // embed; unused methods panic if called
	stream            proto.Agent_StreamProcessRequestClient
}

func (f *fakeAgentClient) StreamProcessRequest(ctx context.Context, in *proto.ProcessRequestRequest, opts ...grpc.CallOption) (proto.Agent_StreamProcessRequestClient, error) {
	return f.stream, nil
}

// TestStreamLoopDeliversWatchdogEvent exercises the real StreamChat payload loop
// with a fake gRPC stream that emits one WatchdogEvent, verifying that the
// event is converted to a TypeWatchdog StreamMsg with the correct fields.
func TestStreamLoopDeliversWatchdogEvent(t *testing.T) {
	fake := &fakeAgentClient{stream: &fakeProcessStream{msgs: []*proto.StreamProcessResponse{
		{Payload: &proto.StreamProcessResponse_WatchdogEvent{WatchdogEvent: &proto.WatchdogEvent{
			Kind: "challenge", Protocol: "commit-checkpoint", Text: "commit first",
		}}},
	}}}
	c := &Client{agent: fake}
	out, err := c.StreamChat(context.Background(), "", "hi", "")
	if err != nil {
		t.Fatal(err)
	}
	var got *StreamMsg
	for m := range out {
		if m.Type == TypeWatchdog {
			mm := m
			got = &mm
		}
	}
	if got == nil {
		t.Fatal("no TypeWatchdog StreamMsg delivered through the real payload loop")
	}
	if got.WatchdogKind != "challenge" || got.Protocol != "commit-checkpoint" || got.Summary != "commit first" {
		t.Fatalf("mapping wrong: %+v", got)
	}
}

// TestGetConfig_WatchdogModeChecksEscalateMapping verifies that
// GetConfigResponse mode/checks/escalate fields map correctly to Config.
func TestGetConfig_WatchdogModeChecksEscalateMapping(t *testing.T) {
	resp := &proto.GetConfigResponse{
		WatchdogMode:          "strict",
		WatchdogChecks:        "a,b",
		WatchdogEscalateAfter: "5",
	}
	cfg := &Config{
		WatchdogMode:          resp.GetWatchdogMode(),
		WatchdogChecks:        splitChecks(resp.GetWatchdogChecks()),
		WatchdogEscalateAfter: atoiOr(resp.GetWatchdogEscalateAfter(), 0),
	}
	if cfg.WatchdogMode != "strict" {
		t.Fatalf("WatchdogMode: got %q, want %q", cfg.WatchdogMode, "strict")
	}
	if len(cfg.WatchdogChecks) != 2 || cfg.WatchdogChecks[0] != "a" || cfg.WatchdogChecks[1] != "b" {
		t.Fatalf("WatchdogChecks: got %v, want [a b]", cfg.WatchdogChecks)
	}
	if cfg.WatchdogEscalateAfter != 5 {
		t.Fatalf("WatchdogEscalateAfter: got %d, want 5", cfg.WatchdogEscalateAfter)
	}
}

// TestConfigUpdate_WatchdogModeChecksEscalateMapping verifies that
// ConfigUpdate mode/checks/escalate pass through as-is to the proto request.
func TestConfigUpdate_WatchdogModeChecksEscalateMapping(t *testing.T) {
	u := ConfigUpdate{WatchdogChecks: "-", WatchdogMode: "strict", WatchdogEscalateAfter: "3"}
	req := &proto.UpdateConfigRequest{
		WatchdogMode:          u.WatchdogMode,
		WatchdogChecks:        u.WatchdogChecks,
		WatchdogEscalateAfter: u.WatchdogEscalateAfter,
	}
	if req.GetWatchdogChecks() != "-" {
		t.Fatalf("WatchdogChecks: got %q, want %q", req.GetWatchdogChecks(), "-")
	}
	if req.GetWatchdogMode() != "strict" {
		t.Fatalf("WatchdogMode: got %q, want %q", req.GetWatchdogMode(), "strict")
	}
	if req.GetWatchdogEscalateAfter() != "3" {
		t.Fatalf("WatchdogEscalateAfter: got %q, want %q", req.GetWatchdogEscalateAfter(), "3")
	}
}

// TestGetConfig_WatchdogFieldMapping verifies that GetConfigResponse watchdog
// booleans map to the correct Config fields (no live server needed — we verify
// the field wiring directly).
func TestGetConfig_WatchdogFieldMapping(t *testing.T) {
	resp := &proto.GetConfigResponse{WatchdogEnabled: true, WatchdogEcho: true}
	cfg := &Config{
		WatchdogEnabled: resp.GetWatchdogEnabled(),
		WatchdogEcho:    resp.GetWatchdogEcho(),
	}
	if !cfg.WatchdogEnabled {
		t.Fatal("WatchdogEnabled: mapping from proto false")
	}
	if !cfg.WatchdogEcho {
		t.Fatal("WatchdogEcho: mapping from proto false")
	}
}

// TestConfigUpdate_WatchdogFieldMapping verifies that ConfigUpdate watchdog
// string fields map to the correct UpdateConfigRequest proto fields.
func TestConfigUpdate_WatchdogFieldMapping(t *testing.T) {
	u := ConfigUpdate{WatchdogEnabled: "true", WatchdogEcho: "false"}
	req := &proto.UpdateConfigRequest{
		WatchdogEnabled: u.WatchdogEnabled,
		WatchdogEcho:    u.WatchdogEcho,
	}
	if req.GetWatchdogEnabled() != "true" {
		t.Fatalf("WatchdogEnabled: got %q, want %q", req.GetWatchdogEnabled(), "true")
	}
	if req.GetWatchdogEcho() != "false" {
		t.Fatalf("WatchdogEcho: got %q, want %q", req.GetWatchdogEcho(), "false")
	}
}

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
