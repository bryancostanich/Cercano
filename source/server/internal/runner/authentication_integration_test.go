package runner

import (
	"cercano/source/server/internal/failurelog"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
)

type recoveryEvents struct{ events []llm.StreamEvent }

func (s *recoveryEvents) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) == 0 {
		return llm.StreamEvent{}, false, nil
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, true, nil
}
func (s *recoveryEvents) Close() error { return nil }

type onceThenLogin struct{ calls int }

func (p *onceThenLogin) Name() string { return "anthropic" }
func (p *onceThenLogin) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *onceThenLogin) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("streaming test")
}
func (p *onceThenLogin) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	if p.calls == 1 {
		return &recoveryEvents{events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventToolUseStart, ToolUseID: "read-once", ToolName: "Read", ToolInputRaw: json.RawMessage(`{}`)}, {Type: llm.EventToolUseStop}, {Type: llm.EventMessageStop, StopReason: "tool_use"}}}, nil
	}
	if p.calls == 2 {
		return nil, &llm.CredentialError{Class: llm.ErrLoginRequired, Provider: "anthropic", Profile: "work", Method: llm.AuthSubscription, Reason: llm.CredentialExpired}
	}
	return &endTurnReader{}, nil
}

type countedRecoveryTool struct {
	testTool
	calls *atomic.Int32
}

func (t countedRecoveryTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	t.calls.Add(1)
	return &agenttools.Result{Type: agenttools.ResultText, Text: "completed once"}, nil
}
func TestRecoveryDoesNotReplayCompletedTools(t *testing.T) {
	for _, choice := range []llm.AuthDecision{llm.AuthLogin, llm.AuthFallback, llm.AuthCancel} {
		t.Run(string(choice), func(t *testing.T) {
			primary := &onceThenLogin{}
			open := &spyProvider{}
			wrapped := resilience.New(primary, resilience.Options{})
			deps := buildDeps(wrapped)
			deps.Providers = &fakeResolver{prov: wrapped, open: open}
			var effects atomic.Int32
			deps.Tools.Registry().Register(countedRecoveryTool{testTool: testTool{name: "Read", perm: agenttools.PermR}, calls: &effects})
			prompts := 0
			_, err := New(deps).RunTurn(context.Background(), Request{ConversationID: "recovery", Input: "read once", WorkDir: t.TempDir(), AuthRecovery: func(_ context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
				prompts++
				if effects.Load() != 1 || primary.calls != 2 || len(open.requests) != 0 {
					t.Errorf("gate did not pause at second inference: %+v", c)
				}
				if choice == llm.AuthFallback && c.Fallback == "" {
					t.Error("configured local fallback not offered")
				}
				return choice, nil
			}}, noopSink{}, nil, nil)
			if choice == llm.AuthCancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if effects.Load() != 1 || prompts != 1 {
				t.Fatalf("replayed work: effects=%d prompts=%d", effects.Load(), prompts)
			}
			if choice == llm.AuthFallback {
				if len(open.requests) != 1 || primary.calls != 2 {
					t.Fatalf("fallback calls=%d primary=%d", len(open.requests), primary.calls)
				}
			} else if len(open.requests) != 0 {
				t.Fatal("unapproved fallback")
			}
		})
	}
}

type diagnosticAuthProvider struct{ onceThenLogin }

func (p *diagnosticAuthProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, &llm.CredentialError{Class: llm.ErrLoginRequired, Provider: "anthropic", Profile: "work", Method: llm.AuthSubscription, Reason: llm.CredentialRejected, Cause: errors.New("synthetic-secret-token")}
}
func TestAuthenticationFailureLogDoesNotPersistCredentialCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failures.jsonl")
	writer, err := failurelog.NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	deps := buildDeps(resilience.New(&diagnosticAuthProvider{}, resilience.Options{}))
	deps.FailureLog = writer
	_, err = New(deps).RunTurn(context.Background(), Request{ConversationID: "safe-log", Input: "hello", WorkDir: t.TempDir()}, noopSink{}, nil, nil)
	if err == nil {
		t.Fatal("expected authentication failure")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "login_required") || strings.Contains(string(data), "synthetic-secret-token") {
		t.Fatalf("unsafe or missing failure record: %s", data)
	}
}
