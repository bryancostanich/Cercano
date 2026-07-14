package anthropic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"cercano/source/server/internal/llm"
)

// WithSessionID attaches a conversation/session ID to ctx so the meridian
// authenticator can emit opencode-style identification headers per request.
// Thin alias over the provider-neutral llm.WithSessionID. Meridian-specific;
// removed with the meridian route.
func WithSessionID(ctx context.Context, id string) context.Context {
	return llm.WithSessionID(ctx, id)
}

// meridianAuth spoofs an OpenCode identity so the local Meridian OAuth bridge
// routes through its OpenCode adapter (4-turn SDK cap instead of the default
// 3, which would break our 10-round tool loop). Stateless.
//
// LEGACY — deleted along with the meridian route once the subscription route
// replaces it. This whole file goes with Phase 4.
//
// TODO(cercano-native-bridge-adapter): the opencode-* header set is a borrowed
// identity — Cercano claims to be OpenCode. See docs/agent/README.md.
type meridianAuth struct{}

func (meridianAuth) decorate(r *http.Request) error {
	sid := llm.SessionIDFromContext(r.Context())
	if sid == "" {
		// Never send a session-less request through Meridian: its OpenCode
		// adapter falls back to matching the conversation by a content
		// fingerprint (cwd + first user message), which collides across
		// concurrent conversations with templated prompts and cross-delivers
		// their turns. A fresh random id gives an unstamped call its own
		// isolated lineage instead.
		sid = "anon-" + newHexToken()
	}
	r.Header.Set("x-opencode-session", sid)
	r.Header.Set("x-opencode-request", newMessageID())
	r.Header.Set("x-opencode-agent-mode", "primary")
	// An independent session (dispatch subagent / one-shot) additionally tells
	// Meridian to skip lineage matching: its adapter treats a requestSource of
	// subagent-*/fork-* as {type:"diverged"} and never resumes a cached
	// session. Second isolation layer over the unique id.
	if llm.IsIndependentSession(r.Context()) {
		r.Header.Set("x-meridian-source", "subagent-"+sid)
	}
	return nil
}

func newMessageID() string { return "msg-" + newHexToken() }

func newHexToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
