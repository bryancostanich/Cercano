package anthropic

import (
	"context"
	"fmt"
	"net/http"
)

// claudeCodeIdentity is the system-prompt identity block Anthropic requires on
// subscription-authenticated Messages calls — the same string Claude Code
// sends. It must be the first system block. Retained deliberately as a
// ToS-gray dependency (see docs/features/replace-meridian/design.md);
// verified live 2026-07-13.
const claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

// oauthBetaHeader is the anthropic-beta value that unlocks subscription-OAuth
// access to /v1/messages (verified live 2026-07-13).
const oauthBetaHeader = "oauth-2025-04-20"

// TokenSource supplies a valid, auto-refreshing subscription bearer token.
// anthropicauth.Source satisfies it structurally, keeping this package free of
// a direct dependency on the auth package.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// subscriptionAuth authenticates with a Claude Max/Pro subscription: a fresh
// Bearer token per request (rotating, from TokenSource) plus the oauth beta
// header. It strips the SDK's placeholder x-api-key so only the Bearer
// credential reaches api.anthropic.com.
type subscriptionAuth struct {
	tokens TokenSource
}

func (a *subscriptionAuth) decorate(r *http.Request) error {
	if a.tokens == nil {
		return fmt.Errorf("anthropic: subscription route requires a token source")
	}
	access, err := a.tokens.Token(r.Context())
	if err != nil {
		return fmt.Errorf("anthropic: subscription token: %w", err)
	}
	r.Header.Del("X-Api-Key")
	r.Header.Set("Authorization", "Bearer "+access)
	r.Header.Set("anthropic-beta", oauthBetaHeader)
	return nil
}
