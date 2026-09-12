package profilechain

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/pkg/config"
	"context"
	"errors"
	"strings"
	"testing"
)

type authRouteProbe struct {
	name    string
	failure error
	calls   []inference.Call
}

func (p *authRouteProbe) Name() string { return p.name }
func (p *authRouteProbe) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *authRouteProbe) Chat(_ context.Context, req inference.Call) (inference.Result, error) {
	p.calls = append(p.calls, req)
	return inference.Result{Model: req.Model}, p.failure
}
func (p *authRouteProbe) StreamChat(_ context.Context, req inference.Call) (inference.Stream, error) {
	p.calls = append(p.calls, req)
	if p.failure != nil {
		return nil, p.failure
	}
	return &authRouteStream{}, nil
}

type authRouteStream struct{ step int }

func (s *authRouteStream) Next() (llm.StreamEvent, bool, error) {
	s.step++
	switch s.step {
	case 1:
		return llm.StreamEvent{Type: llm.EventMessageStart}, true, nil
	case 2:
		return llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "end_turn"}, true, nil
	}
	return llm.StreamEvent{}, false, nil
}
func (*authRouteStream) Close() error { return nil }

func TestNamedAuthRecoveryComposesWithDestinationAndDeepInfraBackup(t *testing.T) {
	for _, destination := range []config.Destination{config.DestinationPrimary, config.DestinationSecondary} {
		for _, streaming := range []bool{false, true} {
			for _, decision := range []llm.AuthDecision{llm.AuthLogin, llm.AuthFallback, llm.AuthCancel} {
				t.Run(string(destination)+"/"+string(decision)+map[bool]string{false: "/chat", true: "/stream"}[streaming], func(t *testing.T) {
					primary := &authRouteProbe{name: "anthropic", failure: &llm.CredentialError{Class: llm.ErrLoginRequired, Provider: "anthropic", Profile: "named-subscription", Method: llm.AuthSubscription, Reason: llm.CredentialExpired}}
					backup := &authRouteProbe{name: "deepinfra"}
					c := config.Defaults()
					c.ActiveCloudProfile = "named-subscription"
					c.BackupCloudProfile = "named-deepinfra"
					c.SecondaryCloudProfile = c.ActiveCloudProfile
					c.SecondaryBackupCloudProfile = c.BackupCloudProfile
					c.CloudProfiles = []config.CloudProfile{{Name: c.ActiveCloudProfile, Flavor: "messages", Route: "subscription", TierOverrides: map[config.CostTier]string{config.CostPremium: "primary-model"}}, {Name: c.BackupCloudProfile, Provider: "deepinfra", Flavor: "chat_completions", TierOverrides: map[config.CostTier]string{config.CostPremium: "deepinfra-model"}}}
					chain, err := Build(c, destination, func(p config.CloudProfile) (inference.Provider, error) {
						raw := primary
						window := 16384
						if p.Name == "named-deepinfra" {
							raw = backup
							window = 131072
						}
						return GuardVision(raw, nil, func(string) modelmetadata.Evidence { return modelmetadata.Evidence{ContextWindow: window} }), nil
					})
					if err != nil {
						t.Fatal(err)
					}
					prompts := 0
					ctx := llm.WithAuthRecovery(context.Background(), func(_ context.Context, challenge llm.AuthChallenge) (llm.AuthDecision, error) {
						prompts++
						if challenge.Profile != "named-subscription" || !strings.Contains(challenge.Fallback, "named-deepinfra") || len(backup.calls) != 0 {
							t.Errorf("incorrect challenge/bypassed pause: %+v", challenge)
						}
						if decision == llm.AuthLogin {
							primary.failure = nil
						}
						return decision, nil
					})
					req := inference.Call{Model: "invocation-override", FallbackTier: "most_capable"}
					call := func(ctx context.Context) (inference.Result, error) {
						if !streaming {
							return chain.Chat(ctx, req)
						}
						stream, err := chain.StreamChat(ctx, req)
						if err != nil {
							return inference.Result{}, err
						}
						defer stream.Close()
						return llm.CollectStream(ctx, stream, nil, nil)
					}
					response, err := call(ctx)
					switch decision {
					case llm.AuthLogin:
						if err != nil || len(primary.calls) != 2 || len(backup.calls) != 0 || primary.calls[1].Model != "invocation-override" {
							t.Fatalf("live login did not preserve call: %v", err)
						}
					case llm.AuthCancel:
						if !errors.Is(err, context.Canceled) || len(backup.calls) != 0 {
							t.Fatalf("cancel invoked backup: %v", err)
						}
					case llm.AuthFallback:
						if err != nil || len(backup.calls) != 1 || backup.calls[0].Model != "deepinfra-model" || backup.calls[0].Tier != "most_capable" || response.Route == nil || response.Route.Profile != "named-deepinfra" || response.Route.Destination != string(destination) {
							t.Fatalf("wrong fallback: %+v %v", response, err)
						}
						target := inference.TargetForContext(ctx, chain, req)
						if target.Profile != "named-deepinfra" || target.ContextWindow != 131072 {
							t.Fatalf("authorized fallback budget detached: %+v", target)
						}
						if _, err := call(ctx); err != nil || len(primary.calls) != 1 || len(backup.calls) != 2 {
							t.Fatalf("request-owned route was lost: %v", err)
						}
						fresh := llm.WithAuthRecovery(context.Background(), func(context.Context, llm.AuthChallenge) (llm.AuthDecision, error) { return llm.AuthCancel, nil })
						if _, err := call(fresh); !errors.Is(err, context.Canceled) || len(backup.calls) != 2 {
							t.Fatal("fallback permission leaked into another request")
						}
					}
					if prompts != 1 {
						t.Fatalf("prompts=%d", prompts)
					}
				})
			}
		}
	}
}

func TestDeepInfraAPIKeyFailureDoesNotLaunchSubscriptionLogin(t *testing.T) {
	primary := &authRouteProbe{name: "deepinfra", failure: &llm.Error{Class: llm.ErrAuth, Provider: "deepinfra", StatusCode: 401}}
	backup := &authRouteProbe{name: "backup"}
	c := config.Defaults()
	c.SecondaryCloudProfile = "deepinfra-key"
	c.SecondaryBackupCloudProfile = "backup-key"
	c.CloudProfiles = []config.CloudProfile{{Name: "deepinfra-key", Provider: "deepinfra"}, {Name: "backup-key", Provider: "deepinfra"}}
	chain, err := Build(c, config.DestinationSecondary, func(p config.CloudProfile) (inference.Provider, error) {
		if p.Name == "deepinfra-key" {
			return primary, nil
		}
		return backup, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := llm.WithAuthRecovery(context.Background(), func(context.Context, llm.AuthChallenge) (llm.AuthDecision, error) {
		t.Error("API key failure requested subscription login")
		return llm.AuthCancel, nil
	})
	if _, err := chain.Chat(ctx, inference.Call{Tier: "most_capable"}); err != nil || len(backup.calls) != 1 {
		t.Fatalf("ordinary API-key policy changed: %v", err)
	}
}
