package dispatch

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

type startupProbeProvider struct {
	echoProvider
	err   error
	calls int
	model string
}

func (p *startupProbeProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.calls++
	p.model = req.Model
	if p.err != nil {
		return llm.ChatResponse{}, p.err
	}
	return p.echoProvider.Chat(ctx, req)
}
func TestDispatchLocalStartupFallback(t *testing.T) {
	local := &startupProbeProvider{err: &llm.LocalStartupError{Provider: "llama_server", Model: "glm-local", Err: errors.New("memory guard refused startup")}}
	cloud := &startupProbeProvider{}
	e := NewEngine(func() inference.Tiers { return inference.Tiers{Open: local, Cloud: cloud} }, func() locus.Mode { return locus.CloudPrimary }, nil)
	e.SetModelFor(func(cloud bool, _ config.Tier) string {
		if cloud {
			return "claude-sonnet-4-6"
		}
		return "glm-local"
	})
	got, err := e.Dispatch(context.Background(), Spec{LocalOffload: true, Role: RoleCoproc, Prompt: "hello"})
	if err != nil {
		t.Fatalf("startup failure stranded dispatch: %v", err)
	}
	if !got.IsCloud || got.Model != "claude-sonnet-4-6" || cloud.model != "claude-sonnet-4-6" || local.calls != 1 || cloud.calls != 1 {
		t.Fatalf("wrong fallback attribution/result: %+v local=%d cloud=%d model=%s", got, local.calls, cloud.calls, cloud.model)
	}
}

func startupFailure() error {
	return &llm.LocalStartupError{Provider: "llama_server", Model: "glm-local", Err: errors.New("memory guard refused startup")}
}

func TestStartupFallbackPolicy(t *testing.T) {
	for _, tt := range []struct {
		name      string
		mode      locus.Mode
		role      Role
		err       error
		cloud     bool
		wantCloud bool
	}{
		{"coproc cloud primary", locus.CloudPrimary, RoleCoproc, startupFailure(), true, true},
		{"open primary", locus.OpenPrimary, RoleMain, startupFailure(), true, true},
		{"open only", locus.OpenOnly, RoleCoproc, startupFailure(), true, false},
		{"cloud absent", locus.CloudPrimary, RoleCoproc, startupFailure(), false, false},
		{"generic error", locus.CloudPrimary, RoleCoproc, errors.New("HTTP 500"), true, false},
		{"cancelled", locus.CloudPrimary, RoleCoproc, &llm.LocalStartupError{Err: context.Canceled}, true, false},
		{"deadline", locus.CloudPrimary, RoleCoproc, &llm.LocalStartupError{Err: context.DeadlineExceeded}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			local, cloud := &startupProbeProvider{err: tt.err}, &startupProbeProvider{}
			tiers := inference.Tiers{Open: local}
			if tt.cloud {
				tiers.Cloud = cloud
			}
			e := NewEngine(func() inference.Tiers { return tiers }, func() locus.Mode { return tt.mode }, nil)
			e.SetModelFor(func(cloud bool, _ config.Tier) string {
				if cloud {
					return "claude-sonnet-4-6"
				}
				return "glm-local"
			})
			_, err := e.Dispatch(context.Background(), Spec{LocalOffload: true, Role: tt.role, Prompt: "hello"})
			if tt.wantCloud {
				if err != nil || cloud.calls != 1 {
					t.Fatalf("fallback: calls=%d err=%v", cloud.calls, err)
				}
			} else if err == nil || cloud.calls != 0 {
				t.Fatalf("must surface local error without cloud: calls=%d err=%v", cloud.calls, err)
			}
		})
	}
}

type probeStream struct {
	step int
	err  error
}

func (s *probeStream) Next() (llm.StreamEvent, bool, error) {
	s.step++
	if s.step == 1 {
		return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "partial"}, true, nil
	}
	return llm.StreamEvent{}, false, s.err
}
func (*probeStream) Close() error { return nil }

type streamingStartupProbe struct {
	startupProbeProvider
	stream llm.StreamReader
}

func (p *streamingStartupProbe) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	p.model = req.Model
	return p.stream, p.err
}

func TestStartupFallbackStreaming(t *testing.T) {
	local := &streamingStartupProbe{startupProbeProvider: startupProbeProvider{err: startupFailure()}}
	cloud := &streamingStartupProbe{stream: &probeStream{}}
	p := &startupFallback{local: local, cloud: cloud, model: "claude-sonnet-4-6", tier: config.TierFastLightText}
	for i := 0; i < 2; i++ {
		s, err := p.StreamChat(context.Background(), llm.ChatRequest{Model: "glm-local"})
		if err != nil || s == nil {
			t.Fatalf("stream %d: %v", i, err)
		}
	}
	if local.calls != 1 || cloud.calls != 2 || cloud.model != "claude-sonnet-4-6" {
		t.Fatalf("wrong sticky route: local=%d cloud=%d model=%s", local.calls, cloud.calls, cloud.model)
	}
}

func TestStartupFallbackNeverRetriesOpenedStream(t *testing.T) {
	local := &streamingStartupProbe{stream: &probeStream{err: startupFailure()}}
	cloud := &streamingStartupProbe{}
	p := &startupFallback{local: local, cloud: cloud, model: "claude-sonnet-4-6"}
	s, err := p.StreamChat(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ev, _, err := s.Next()
	if err != nil || ev.TextDelta != "partial" {
		t.Fatal("missing partial output")
	}
	_, _, err = s.Next()
	if err == nil || cloud.calls != 0 {
		t.Fatalf("opened stream was retried: calls=%d err=%v", cloud.calls, err)
	}
}

func TestStartupFallbackDoesNotRestartAgenticRunner(t *testing.T) {
	local, cloud := &startupProbeProvider{}, &startupProbeProvider{}
	e := NewEngine(func() inference.Tiers { return inference.Tiers{Open: local, Cloud: cloud} }, func() locus.Mode { return locus.CloudPrimary }, nil)
	e.SetModelFor(func(cloud bool, _ config.Tier) string {
		if cloud {
			return "claude-sonnet-4-6"
		}
		return "glm-local"
	})
	runs, toolExecutions := 0, 0
	e.SetAgenticRunner(func(ctx context.Context, spec Spec, sel inference.Selection, model string) (Result, error) {
		runs++
		req := llm.ChatRequest{Model: model, Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "task"}}}}}
		if _, err := sel.Provider.Chat(ctx, req); err != nil {
			return Result{}, err
		}
		toolExecutions++ // stand-in for an already-completed external action
		req.Messages = append(req.Messages, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "completed action result"}}})
		local.err = startupFailure()
		resp, err := sel.Provider.Chat(ctx, req)
		if err != nil {
			return Result{}, err
		}
		if resp.Blocks[0].Text != "echo: completed action result" {
			t.Fatal("history was lost")
		}
		return Result{Text: resp.Blocks[0].Text, Model: model, IsCloud: sel.IsCloud}, nil
	})
	got, err := e.Dispatch(context.Background(), Spec{LocalOffload: true, Mode: Agentic, Role: RoleCoproc})
	if err != nil || runs != 1 || toolExecutions != 1 || !got.IsCloud || got.Model != "claude-sonnet-4-6" {
		t.Fatalf("runs=%d actions=%d result=%+v err=%v", runs, toolExecutions, got, err)
	}
}

func TestStartupFallbackRejectsUnknownOrOversizedCloudRequest(t *testing.T) {
	for _, model := range []string{"unknown-cloud-model", "claude-sonnet-4-6"} {
		local, cloud := &startupProbeProvider{err: startupFailure()}, &startupProbeProvider{}
		p := &startupFallback{local: local, cloud: cloud, model: model}
		_, err := p.Chat(context.Background(), llm.ChatRequest{MaxTokens: 300000})
		if err == nil || cloud.calls != 0 {
			t.Fatalf("model=%s calls=%d err=%v", model, cloud.calls, err)
		}
	}
}

func TestClassifiedStartupFallbackFinalPrimary(t *testing.T) {
	for _, final := range []config.Destination{config.DestinationPrimary, config.DestinationLocal} {
		t.Run(string(final), func(t *testing.T) {
			local, cloud := &startupProbeProvider{err: startupFailure()}, &startupProbeProvider{}
			c := config.Config{SecondaryRedirect: final, TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskDispatch: {Destination: config.DestinationSecondary, Quality: config.CostStandard}}}
			e := NewEngine(func() inference.Tiers {
				return inference.Tiers{Open: local, Cloud: cloud, TaskFor: c.TaskAssignment, ResolveDestination: c.ResolveDestination}
			}, func() locus.Mode { return locus.OpenPrimary }, nil)
			e.SetModelFor(func(cloud bool, _ config.Tier) string {
				if cloud {
					return "claude-sonnet-4-6"
				}
				return "glm-local"
			})
			_, err := e.Dispatch(context.Background(), Spec{RoutingTask: config.TaskDispatch, Prompt: "fixture"})
			if final == config.DestinationPrimary {
				if err != nil || cloud.calls != 1 {
					t.Fatalf("final Primary fallback calls=%d err=%v", cloud.calls, err)
				}
			} else if err == nil || cloud.calls != 0 {
				t.Fatalf("final Local escaped: %v calls=%d", err, cloud.calls)
			}
		})
	}
}
