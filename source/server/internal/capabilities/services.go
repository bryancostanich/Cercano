package capabilities

import (
	"context"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/modelbudget"
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

	// DispatchTarget resolves the concrete target a one-shot dispatch would use
	// without sending a model request. Capabilities that construct large prompts
	// use it to budget before calling Dispatch. Nil means target budgeting is
	// unavailable and callers should fail clearly rather than guess.
	DispatchTarget func(ctx context.Context, spec dispatch.Spec) (modelbudget.Target, error)

	// EnterProfile switches one conversation's active capability profile by name
	// ("plan", "default", …). It is how the suggest_plan capability flips the
	// session into the read-only planning fence once the user approves the
	// suggestion at the confirm gate. The convID scopes the switch to the
	// calling conversation so one client entering planning mode never fences
	// another attached client. A func hook (not the agent.ProfileBroker type)
	// keeps this package free of an agent import, matching Dispatch. Nil until
	// wired by the server; suggest_plan errors clearly if it is nil.
	EnterProfile func(convID, name string) error

	// RestartAgent bounces the singleton agent process: it drains in-flight
	// turns, stops runtime children, and exits, letting the CLI's reconnect loop
	// auto-launch a fresh agent. It is how the restart_agent capability performs
	// a clean self-bounce (e.g. after the agent binary is rebuilt). The hook
	// returns after scheduling the bounce, not after the process exits — the
	// caller's tool_result must flush before the socket drops. A func hook keeps
	// this package free of a server import, matching Dispatch/EnterProfile. Nil
	// until wired by the server; restart_agent errors clearly if it is nil.
	RestartAgent func(reason string) error

	// Vision resolves an image attachment by conversation-scoped ID and asks the
	// configured vision model a focused question about it. It backs the
	// inspect_image capability. Nil means vision-as-tool is not wired for this
	// deployment; inspect_image reports vision unavailable rather than erroring.
	// A func hook keeps this package free of a visionattach/inference import,
	// matching Dispatch/EnterProfile/RestartAgent.
	Vision VisionService
}

// VisionService is the seam inspect_image calls through. It separates presence
// ("is this image still in memory?") from inspection ("ask the vision model")
// so the tool can give a clear reattach message on a stale/unknown ID without
// spinning up a model call. Implemented by the server over the per-conversation
// attachment store plus the resolved vision-tier provider; nil when
// vision-as-tool is not configured.
type VisionService interface {
	// Available reports whether a vision model is configured and reachable for
	// the current locus/runtime. False means inspect_image should return an
	// "unavailable" result rather than attempt a call.
	Available() bool
	// Lookup reports whether an image with imageID is currently held for convID.
	// A miss is the expected condition after restart/resume or for an unknown
	// ID; the tool turns it into a clear reattach message.
	Lookup(convID, imageID string) (found bool)
	// Inspect asks the vision model question about the image and returns the
	// answer envelope. It is only reached once Lookup has confirmed presence and
	// Available reports true. Wired in a later phase; a nil-returning stub is
	// acceptable while the tool skeleton lands.
	Inspect(ctx context.Context, convID, imageID, question string) (VisionAnswer, error)
}

// VisionAnswer is the structured result of one inspect_image call, rendered into
// the tool's text envelope.
type VisionAnswer struct {
	Answer     string // the vision model's free-text answer
	Confidence string // optional, model-reported or heuristic; may be empty
	Source     string // which model/provider answered, e.g. "open:gemma-3-4b-it"
}

// MainProvider returns the provider for a turn: cloud when isCloud and a cloud
// provider is configured, otherwise local.
func (s Services) MainProvider(isCloud bool) inference.Provider {
	if isCloud && s.CloudProvider != nil {
		return s.CloudProvider
	}
	return s.OpenProvider
}
