package runner

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/chatroute"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

// sessionRoutePolicyConfig builds a config with two Anthropic-flavor cloud
// profiles whose PHYSICAL identities differ: "work" is a subscription-route
// profile (endpoint https://api.anthropic.com) and "other" is a direct profile
// on a custom gateway (https://gateway.example.com). Same vendor and model
// lineup, different endpoint — i.e. a different account. The policy identity
// is (provider, endpoint, model, placement), so a model name allowed on one
// account is NOT permission to use it from another.
func sessionRoutePolicyConfig() *fakeConfig {
	return &fakeConfig{cfg: config.Config{
		LocusMode: "cloud_primary",
		CloudProfiles: []config.CloudProfile{
			{Name: "work", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription},
			{Name: "other", Flavor: cloudfactory.FlavorMessages, BaseURL: "https://gateway.example.com"},
		},
	}}
}

// policyAuthority returns an enterprise authorization authority that allows
// exactly the listed routes (all four identity fields must match), mirroring
// modelpolicy.Allowed semantics.
func policyAuthority(routes ...v1.Route) modelpolicy.Authority {
	policy := v1.Policy{AllowedRoutes: routes}
	return modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
		if modelpolicy.Allowed(policy, a) {
			return nil
		}
		return modelpolicy.Deny(a, "route is not in the enterprise allow-list")
	})
}

func sessionRouteDeps(pinned, normal *spyProvider, cfg *fakeConfig) Deps {
	deps := buildDeps(normal)
	deps.Providers = &sessionResolver{fakeResolver: &fakeResolver{prov: normal, open: normal, cloud: normal}, pinned: pinned}
	deps.Config = cfg
	return deps
}

// TestSessionRoutePolicyAllowedIdentityHonored: an explicit session choice whose
// FULL physical identity is in the allow-list is honored exactly.
func TestSessionRoutePolicyAllowedIdentityHonored(t *testing.T) {
	pinned, normal := &spyProvider{}, &spyProvider{}
	deps := sessionRouteDeps(pinned, normal, sessionRoutePolicyConfig())
	ctx := modelpolicy.WithAuthority(context.Background(), policyAuthority(v1.Route{
		Provider: "anthropic", Endpoint: "https://api.anthropic.com",
		Model: "claude-work", Placement: "external",
	}))
	_, err := New(deps).RunTurn(ctx, Request{
		ConversationID: "session-allowed", Input: "hello", WorkDir: t.TempDir(),
		ChatRoute: &chatroute.Route{Profile: "work", Model: "claude-work"},
	}, noopSink{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normal.requests) != 0 || len(pinned.requests) != 1 || pinned.requests[0].Model != "claude-work" {
		t.Fatalf("allowed session choice not honored exactly: normal=%d pinned=%+v", len(normal.requests), pinned.requests)
	}
}

// TestSessionRoutePolicySameModelDifferentAccountDenied: a model name present in
// the allow-list on a DIFFERENT endpoint is not permission — the requested
// identity is denied, and the denial is a hard failure with no silent fallback.
func TestSessionRoutePolicySameModelDifferentAccountDenied(t *testing.T) {
	pinned, normal := &spyProvider{}, &spyProvider{}
	deps := sessionRouteDeps(pinned, normal, sessionRoutePolicyConfig())
	// "claude-work" is allowed — but only via the gateway endpoint (the
	// "other" account), never via the subscription endpoint the turn requests.
	ctx := modelpolicy.WithAuthority(context.Background(), policyAuthority(v1.Route{
		Provider: "anthropic", Endpoint: "https://gateway.example.com",
		Model: "claude-work", Placement: "external",
	}))
	_, err := New(deps).RunTurn(ctx, Request{
		ConversationID: "session-wrong-account", Input: "hello", WorkDir: t.TempDir(),
		ChatRoute: &chatroute.Route{Profile: "work", Model: "claude-work"},
	}, noopSink{}, nil, nil)
	if !modelpolicy.IsDenial(err) {
		t.Fatalf("wrong-account session choice must be a policy denial, got %v", err)
	}
	if len(pinned.requests) != 0 || len(normal.requests) != 0 {
		t.Fatalf("denied session choice must not reach any provider: pinned=%d normal=%d", len(pinned.requests), len(normal.requests))
	}
	if !strings.Contains(err.Error(), "claude-work") || !strings.Contains(err.Error(), "https://api.anthropic.com") {
		t.Fatalf("denial must name the rejected identity (model + endpoint): %v", err)
	}
}

// TestSessionRoutePolicyModelOverridePartOfIdentity: the per-request
// ModelOverride replaces the session route's model BEFORE validation, so the
// combined identity is checked as a whole — an override to a model the policy
// allows is honored; an override to one it does not is denied.
func TestSessionRoutePolicyModelOverridePartOfIdentity(t *testing.T) {
	authority := policyAuthority(
		v1.Route{Provider: "anthropic", Endpoint: "https://api.anthropic.com", Model: "claude-work", Placement: "external"},
		v1.Route{Provider: "anthropic", Endpoint: "https://api.anthropic.com", Model: "claude-alt", Placement: "external"},
	)
	for _, tc := range []struct {
		name   string
		model  string
		denied bool
	}{
		{"override to allowed model honored", "claude-alt", false},
		{"override to unapproved model denied", "claude-forbidden", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinned, normal := &spyProvider{}, &spyProvider{}
			deps := sessionRouteDeps(pinned, normal, sessionRoutePolicyConfig())
			ctx := modelpolicy.WithAuthority(context.Background(), authority)
			_, err := New(deps).RunTurn(ctx, Request{
				ConversationID: "session-override", Input: "hello", WorkDir: t.TempDir(),
				ChatRoute:     &chatroute.Route{Profile: "work", Model: "claude-work"},
				ModelOverride: tc.model,
			}, noopSink{}, nil, nil)
			if tc.denied {
				if !modelpolicy.IsDenial(err) || !strings.Contains(err.Error(), tc.model) {
					t.Fatalf("override to unapproved model must be denied naming it, got %v", err)
				}
				if len(pinned.requests) != 0 {
					t.Fatalf("denied override reached provider: %+v", pinned.requests)
				}
				return
			}
			if err != nil || len(pinned.requests) != 1 || pinned.requests[0].Model != tc.model {
				t.Fatalf("allowed override not honored: err=%v pinned=%+v", err, pinned.requests)
			}
		})
	}
}

// TestSessionRoutePolicyUnmanagedBypassesAuthorization: with no enterprise
// authority installed, an explicit session choice runs without any policy gate
// — main's unmanaged behavior is preserved.
func TestSessionRoutePolicyUnmanagedBypassesAuthorization(t *testing.T) {
	pinned, normal := &spyProvider{}, &spyProvider{}
	deps := sessionRouteDeps(pinned, normal, sessionRoutePolicyConfig())
	// No authority in the context and none installed process-wide by this
	// package's tests: modelpolicy.Managed is false.
	_, err := New(deps).RunTurn(context.Background(), Request{
		ConversationID: "session-unmanaged", Input: "hello", WorkDir: t.TempDir(),
		ChatRoute: &chatroute.Route{Profile: "work", Model: "claude-anything"},
	}, noopSink{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.requests) != 1 || pinned.requests[0].Model != "claude-anything" {
		t.Fatalf("unmanaged session choice must be honored without a policy gate: %+v", pinned.requests)
	}
}
