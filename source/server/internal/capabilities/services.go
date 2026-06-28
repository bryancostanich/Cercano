package capabilities

import (
	"context"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// Services holds the static collaborators a capability may need. Injected once
// when the registry is built. There is no ProviderSet type — the agent holds
// cloud + local providers as two discrete fields, so Services mirrors that.
type Services struct {
	CloudProvider llm.Provider // may be nil (local-only deployments)
	LocalProvider llm.Provider
	Engine        engine.InferenceEngine
	Config        *config.Config
	Conversations conversation.Store
	ProjectCtx    *projectctx.Loader

	// RunCoproc runs a co-processor prompt through the agent's local pipeline
	// (the equivalent of ProcessRequest with Coproc=true) and returns the
	// model output. Set by the agent server when it builds the registry; used
	// by the co-processor capabilities (Phase 5). May be nil in tests.
	RunCoproc func(ctx context.Context, prompt, projectDir string) (string, error)
}

// MainProvider returns the provider for a turn: cloud when isCloud and a cloud
// provider is configured, otherwise local.
func (s Services) MainProvider(isCloud bool) llm.Provider {
	if isCloud && s.CloudProvider != nil {
		return s.CloudProvider
	}
	return s.LocalProvider
}
