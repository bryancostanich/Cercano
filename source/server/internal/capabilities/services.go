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
}

// MainProvider returns the provider for a turn: cloud when isCloud and a cloud
// provider is configured, otherwise local.
func (s Services) MainProvider(isCloud bool) inference.Provider {
	if isCloud && s.CloudProvider != nil {
		return s.CloudProvider
	}
	return s.OpenProvider
}
