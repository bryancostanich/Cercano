package resilience

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"errors"
	"testing"
)

// Until a request owner explicitly authorizes recovery, login-required failures
// must surface rather than acquiring the default transient/fallback policy.
func TestAuthRecoveryNoImplicitBackup(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		primary := &fakeProvider{name: "anthropic", outcome: []error{&llm.Error{Class: llm.ErrLoginRequired, Provider: "anthropic", Err: &llm.CredentialError{Class: llm.ErrLoginRequired, Profile: "work", Reason: llm.CredentialExpired}}}}
		backup := &fakeProvider{name: "openai"}
		p, _, sleeps := build(primary, backup)
		var err error
		if streaming {
			_, err = p.StreamChat(context.Background(), inference.Call{})
		} else {
			_, err = p.Chat(context.Background(), inference.Call{})
		}
		if llm.ClassOf(err) != llm.ErrLoginRequired || primary.calls != 1 || backup.calls != 0 || len(*sleeps) != 0 {
			t.Fatalf("streaming=%v err=%v primary=%d backup=%d sleeps=%v", streaming, err, primary.calls, backup.calls, *sleeps)
		}
	}
}

func loginRequired(profile string) error {
	return &llm.CredentialError{Class: llm.ErrLoginRequired, Provider: "anthropic", Profile: profile, Method: llm.AuthSubscription, Reason: llm.CredentialExpired}
}
func TestAuthRecoveryChoicesAndBoundedRetry(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, choice := range []llm.AuthDecision{llm.AuthLogin, llm.AuthFallback, llm.AuthCancel} {
			primary := &fakeProvider{name: "anthropic", outcome: []error{loginRequired("work")}}
			backup := &fakeProvider{name: "backup"}
			p, _, _ := build(primary, backup)
			prompts := 0
			ctx := llm.WithAuthRecovery(context.Background(), func(_ context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
				prompts++
				if primary.calls != 1 || backup.calls != 0 || c.Profile != "work" || c.Fallback != "backup" {
					t.Errorf("did not pause before fallback: %+v", c)
				}
				return choice, nil
			})
			var err error
			if streaming {
				var stream inference.Stream
				stream, err = p.StreamChat(ctx, inference.Call{})
				if err == nil {
					_, err = collectStream(t, stream)
					stream.Close()
				}
			} else {
				_, err = p.Chat(ctx, inference.Call{})
			}
			if prompts != 1 {
				t.Fatalf("prompts=%d", prompts)
			}
			switch choice {
			case llm.AuthLogin:
				if err != nil || primary.calls != 2 || backup.calls != 0 {
					t.Fatalf("login retry: %v", err)
				}
			case llm.AuthFallback:
				if err != nil || primary.calls != 1 || backup.calls != 1 {
					t.Fatalf("fallback: %v", err)
				}
			case llm.AuthCancel:
				if !errors.Is(err, context.Canceled) || backup.calls != 0 {
					t.Fatalf("cancel: %v", err)
				}
			}
		}
	}
	primary := &fakeProvider{name: "anthropic", outcome: []error{loginRequired("work"), loginRequired("work")}}
	p, _, _ := build(primary, nil)
	prompts := 0
	ctx := llm.WithAuthRecovery(context.Background(), func(context.Context, llm.AuthChallenge) (llm.AuthDecision, error) {
		prompts++
		return llm.AuthLogin, nil
	})
	if _, err := p.Chat(ctx, inference.Call{}); llm.ClassOf(err) != llm.ErrLoginRequired || prompts != 1 || primary.calls != 2 {
		t.Fatalf("unbounded recovery: prompts=%d calls=%d err=%v", prompts, primary.calls, err)
	}
}
func TestAuthRecoveryAfterPartialOutputDoesNotReplay(t *testing.T) {
	calls := 0
	stream := &fakeStream{events: []llm.StreamEvent{{Type: llm.EventTextDelta, TextDelta: "partial"}}, err: loginRequired("work")}
	primary := &fakeProvider{name: "anthropic", streamOverride: func(context.Context, inference.Call) (inference.Stream, error) { calls++; return stream, nil }}
	p, _, _ := build(primary, nil)
	ctx := llm.WithAuthRecovery(context.Background(), func(_ context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
		if c.RetrySafe || c.Fallback != "" || !stream.closed {
			t.Errorf("unsafe gate: %+v", c)
		}
		return llm.AuthLogin, nil
	})
	reader, err := p.StreamChat(ctx, inference.Call{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.Next()
	_, _, err = reader.Next()
	if err == nil || calls != 1 || llm.Retryable(llm.ClassOf(err)) {
		t.Fatalf("replayed partial request: calls=%d err=%v", calls, err)
	}
}
func TestAuthRecoveryOnSelectedBackup(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{quotaErr("primary")}}
	backup := &fakeProvider{name: "anthropic", outcome: []error{loginRequired("backup")}}
	p, _, _ := build(primary, backup)
	prompts := 0
	ctx := llm.WithAuthRecovery(context.Background(), func(_ context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
		prompts++
		if c.Profile != "backup" || c.Fallback != "" {
			t.Errorf("wrong backup gate: %+v", c)
		}
		return llm.AuthLogin, nil
	})
	stream, err := p.StreamChat(ctx, inference.Call{})
	if err == nil {
		_, err = collectStream(t, stream)
		stream.Close()
	}
	if err != nil || prompts != 1 || primary.calls != 1 || backup.calls != 2 {
		t.Fatalf("backup recovery: prompts=%d primary=%d backup=%d err=%v", prompts, primary.calls, backup.calls, err)
	}
}

func TestExplicitFallbackIsScopedToOneTurnContext(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		primary := &fakeProvider{name: "primary", outcome: []error{loginRequired("work")}}
		backup := &fakeProvider{name: "backup"}
		p, _, _ := build(primary, backup)
		ctx := llm.WithAuthRecovery(context.Background(), func(context.Context, llm.AuthChallenge) (llm.AuthDecision, error) { return llm.AuthFallback, nil })
		call := func(ctx context.Context) {
			t.Helper()
			if streaming {
				s, e := p.StreamChat(ctx, inference.Call{})
				if e != nil {
					t.Fatal(e)
				}
				_, e = collectStream(t, s)
				s.Close()
				if e != nil {
					t.Fatal(e)
				}
			} else {
				if _, e := p.Chat(ctx, inference.Call{}); e != nil {
					t.Fatal(e)
				}
			}
		}
		call(ctx)
		call(ctx)
		if primary.calls != 1 || backup.calls != 2 {
			t.Fatalf("selected route lost within turn: primary=%d backup=%d", primary.calls, backup.calls)
		}
		call(context.Background())
		if primary.calls != 2 || backup.calls != 2 {
			t.Fatal("fallback choice leaked to another turn")
		}
	}
}

func TestAuthFallbackDoesNotOfferIncompatibleBackup(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{loginRequired("work")}}
	backup := &fakeProvider{name: "no-vision"}
	p, _, _ := build(primary, backup)
	ctx := llm.WithAuthRecovery(context.Background(), func(_ context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
		if c.Fallback != "" {
			t.Errorf("non-vision fallback offered for image request: %q", c.Fallback)
		}
		return llm.AuthCancel, nil
	})
	_, _ = p.Chat(ctx, inference.Call{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockImage}}}}})
}
