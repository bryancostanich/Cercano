package runner

import (
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
	"cercano/source/server/pkg/config"
	"context"
	"errors"
	"strings"
	"testing"
)

type partialNetworkProvider struct{ calls int }

func (p *partialNetworkProvider) Name() string { return "partial-network" }
func (p *partialNetworkProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *partialNetworkProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("stream expected")
}
func (p *partialNetworkProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	return &partialNetworkStream{}, nil
}

type partialNetworkStream struct {
	emitted bool
	started bool
}

func (s *partialNetworkStream) Close() error { return nil }
func (s *partialNetworkStream) Next() (llm.StreamEvent, bool, error) {
	if !s.started {
		s.started = true
		return llm.StreamEvent{Type: llm.EventMessageStart}, true, nil
	}
	if !s.emitted {
		s.emitted = true
		return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "visible-prefix"}, true, nil
	}
	return llm.StreamEvent{}, false, &llm.Error{Class: llm.ErrNetwork, Err: errors.New("fixture stream reset")}
}

func TestRoutingContractRunnerDoesNotReplayVisibleOutput(t *testing.T) {
	p := &partialNetworkProvider{}
	deps := buildDeps(p)
	sink := &captureSink{}
	_, err := New(deps).RunTurn(context.Background(), Request{Input: "fixture", ConversationID: "replay-probe", WorkDir: t.TempDir()}, sink, nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	var text strings.Builder
	for _, event := range sink.events {
		if event.Kind == EventToken {
			text.WriteString(event.Text)
		}
	}
	t.Logf("provider calls=%d visible output=%q", p.calls, text.String())
	if p.calls != 1 || text.String() != "visible-prefix" {
		t.Errorf("runner replayed after visible output: calls=%d output=%q", p.calls, text.String())
	}
}

type executedToolProvider struct{ partialNetworkProvider }

func (p *executedToolProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	if p.calls%2 == 0 {
		return nil, &llm.Error{Class: llm.ErrNetwork, Err: errors.New("after tool")}
	}
	return &replayToolStream{}, nil
}

type replayToolStream struct{ index int }

func (*replayToolStream) Close() error { return nil }
func (s *replayToolStream) Next() (llm.StreamEvent, bool, error) {
	events := []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventToolUseStart, ToolUseID: "one", ToolName: "probe"}, {Type: llm.EventToolUseInputDelta, TextDelta: "{}"}, {Type: llm.EventToolUseStop}, {Type: llm.EventMessageStop, StopReason: "tool_use"}}
	if s.index == len(events) {
		return llm.StreamEvent{}, false, nil
	}
	e := events[s.index]
	s.index++
	return e, true, nil
}
func TestRoutingContractRunnerDoesNotReplayExecutedTool(t *testing.T) {
	p := &executedToolProvider{}
	deps := buildDeps(p)
	reg := agenttools.NewRegistry()
	if err := reg.Register(testTool{name: "probe", perm: agenttools.PermR}); err != nil {
		t.Fatal(err)
	}
	deps.Tools = &fakeToolSvc{reg: reg}
	sink := &captureSink{}
	_, err := New(deps).RunTurn(context.Background(), Request{Input: "probe", ConversationID: "tool-replay", WorkDir: t.TempDir()}, sink, nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	executions := 0
	for _, e := range sink.events {
		if e.Kind == EventToolExecStart {
			executions++
		}
	}
	t.Logf("provider calls=%d tool executions=%d", p.calls, executions)
	if executions != 1 || p.calls != 2 {
		t.Errorf("replayed executed tool: calls=%d executions=%d", p.calls, executions)
	}
}

func TestRoutingContractVisibleOutputBlocksCrossDestinationFallback(t *testing.T) {
	p := &partialNetworkProvider{}
	backup := &spyProvider{}
	deps := buildDeps(p)
	deps.Config = &fakeConfig{cfg: config.Config{LocusMode: "cloud_primary"}}
	deps.Providers = &fakeResolver{prov: p, cloud: p, open: backup, isCloud: true, isCloudSet: true}
	_, err := New(deps).RunTurn(context.Background(), Request{Input: "fixture", ConversationID: "cross-replay", WorkDir: t.TempDir()}, &captureSink{}, nil, nil)
	if err == nil || p.calls != 1 || len(backup.requests) != 0 {
		t.Fatalf("err=%v primary calls=%d fallback calls=%d", err, p.calls, len(backup.requests))
	}
}

func TestRoutingContractSecondaryChatCannotFallBackToLocal(t *testing.T) {
	primary := &busyProvider{}
	local := &spyProvider{}
	deps := buildDeps(primary)
	deps.Config = &fakeConfig{cfg: config.Config{LocusMode: "cloud_primary", TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationSecondary}}}}
	deps.Providers = &fakeResolver{prov: primary, cloud: primary, open: local, isCloud: true, isCloudSet: true}
	_, err := New(deps).RunTurn(context.Background(), Request{Input: "fixture", ConversationID: "secondary", WorkDir: t.TempDir()}, &captureSink{}, nil, nil)
	if err == nil || len(local.requests) != 0 {
		t.Fatalf("Secondary escaped: err=%v Local calls=%d", err, len(local.requests))
	}
}

func TestRedirectedChatUsesFinalFallbackPolicy(t *testing.T) {
	for _, tt := range []struct {
		name          string
		origin, final config.Destination
		fallback      bool
	}{
		{"secondary-primary", config.DestinationSecondary, config.DestinationPrimary, true},
		{"local-primary", config.DestinationLocal, config.DestinationPrimary, true},
		{"primary-secondary", config.DestinationPrimary, config.DestinationSecondary, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			primary, local := &busyProvider{}, &spyProvider{}
			assigned := inference.WithTaskRoute(primary, config.TaskChat, config.TaskAssignment{Destination: tt.origin, Quality: config.CostStandard}, tt.final, "fake-cloud-model")
			p := usage.Wrap(assigned, "main", true, nil)
			deps := buildDeps(p)
			deps.Config = &fakeConfig{cfg: config.Config{LocusMode: "cloud_primary"}}
			deps.Providers = &fakeResolver{prov: p, cloud: primary, open: local, isCloud: true, isCloudSet: true}
			_, err := New(deps).RunTurn(context.Background(), Request{Input: "fixture", ConversationID: tt.name, WorkDir: t.TempDir()}, &captureSink{}, nil, nil)
			if (len(local.requests) > 0) != tt.fallback {
				t.Fatalf("fallback calls=%d err=%v", len(local.requests), err)
			}
		})
	}
}
