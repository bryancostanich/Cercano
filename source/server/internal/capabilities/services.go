package capabilities

import (
	"context"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/inference"
	"cercano/source/server/pkg/config"
)

// Services holds the static collaborators a capability may need. Injected once
// when the registry is built. There is no ProviderSet type — the agent holds
// cloud + local providers as two discrete fields, so Services mirrors that.
type Services struct {
	CloudProvider inference.Provider // may be nil (local-only deployments)
	OpenProvider  inference.Provider
	Engine        engine.InferenceEngine
	Config        *config.Config
	Conversations conversation.Store
	ProjectCtx    *projectctx.Loader

	// Dispatch runs an agentic (or one-shot) unit of delegated model work through
	// the unified dispatch engine. Nil until wired by the server.
	Dispatch func(ctx context.Context, spec dispatch.Spec) (dispatch.Result, error)

	// EnterProfile switches the session's active capability profile by name
	// ("plan", "default", …). It is how the suggest_plan capability flips the
	// session into the read-only planning fence once the user approves the
	// suggestion at the confirm gate. A func hook (not the agent.ProfileBroker
	// type) keeps this package free of an agent import, matching Dispatch. Nil
	// until wired by the server; suggest_plan errors clearly if it is nil.
	EnterProfile func(name string) error

	// RestartAgent bounces the singleton agent process: it drains in-flight
	// turns, stops runtime children, and exits, letting the CLI's reconnect loop
	// auto-launch a fresh agent. It is how the restart_agent capability performs
	// a clean self-bounce (e.g. after the agent binary is rebuilt). The hook
	// returns after scheduling the bounce, not after the process exits — the
	// caller's tool_result must flush before the socket drops. A func hook keeps
	// this package free of a server import, matching Dispatch/EnterProfile. Nil
	// until wired by the server; restart_agent errors clearly if it is nil.
	RestartAgent func(reason string) error
}

// MainProvider returns the provider for a turn: cloud when isCloud and a cloud
// provider is configured, otherwise local.
func (s Services) MainProvider(isCloud bool) inference.Provider {
	if isCloud && s.CloudProvider != nil {
		return s.CloudProvider
	}
	return s.OpenProvider
}
