// Package runner executes one conversation turn in isolation. A Core holds no
// process-global mutable state, so many Cores run concurrently in one process
// (embedded mode) or one Core runs per worker process (Phase 5) — the same
// code path either way.
package runner

import (
	"context"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	permissions "cercano/source/server/internal/hostsvc/permissions"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/routinglog"
	"cercano/source/server/internal/watchdog"
)

// TurnHistory is the narrow subset of persistence.Service the runner needs:
// assembly of conversation history before the turn and persistence of each
// message as the turn produces it. Defined here (not as a direct reference to
// persistence.Service) so the runner stays proto-free — persistence.Service
// carries proto-typed RPC methods that would drag pkg/proto into this package.
// persistence.Service satisfies TurnHistory structurally.
type TurnHistory interface {
	// AssembleHistory returns the conversation history to send to the model.
	AssembleHistory(ctx context.Context, convID string) []llm.Message
	// PersistTurn writes one turn message. Best-effort; never blocks.
	PersistTurn(ctx context.Context, convID string, m llm.Message)
	// LoadProjectContext returns the project's .cercano/context.md content, or "".
	LoadProjectContext(workDir string) string
}

// ToolSvc is the narrow subset of tools.Catalog the runner needs: the tool
// registry for the turn's tool loop, and the sub-dispatch helper. Defined here
// to keep the runner proto-free — tools.Catalog's capability-registry methods
// drag in internal/capabilities/mcpadapter (which imports pkg/proto).
// tools.Catalog satisfies ToolSvc structurally.
type ToolSvc interface {
	// Registry returns the current agent tool registry.
	Registry() *agenttools.Registry
	// GrantedRegistry builds the least-privilege sub-registry for a dispatch.
	GrantedRegistry(tools []string) (*agenttools.Registry, []string, []string, error)
}

// Request carries the user-facing inputs for one turn. It deliberately holds NO
// inference.Provider and NO assembled history — the runner resolves the provider and
// assembles history itself (so it works across a process boundary; see the
// plan's load-bearing decision).
type Request struct {
	ConversationID string
	Input          string
	Images         []agent.InlineImage
	WorkDir        string
	Gen            uint64 // this turn's generation, for the host's persist fence
}

// Result is the turn's outcome.
type Result struct {
	FinalText    string
	Model        string
	IsCloud      bool
	InputTokens  int
	OutputTokens int
	Notice       string // e.g. a cross-tier fallback note
}

// TurnRunner executes one turn. RunTurn emits events via sink, gates W/X calls
// via requester, persists via persist (host-fenced), and returns the result.
// It must hold no process-global mutable state.
type TurnRunner interface {
	RunTurn(ctx context.Context, req Request, sink EventSink, requester PermissionRequester, persist PersistFunc) (Result, error)
}

// Deps are the shared, process-wide services the runner consumes. Injected once
// at construction; a worker builds its own set from a config snapshot.
type Deps struct {
	Providers providers.Resolver
	Tools     ToolSvc     // narrow interface; tools.Catalog satisfies it
	Persist   TurnHistory // narrow interface; persistence.Service satisfies it
	Config    cfgsvc.Service
	Perms     permissions.Broker
	Agent     *agent.Agent
	// Watchdog is a LIVE accessor, not a snapshot: the host wires its watchdog
	// (InitWatchdog) and mutates it (UpdateConfig) AFTER the runner is
	// constructed, so the runner must read it at turn time. A nil func, or a
	// func returning nil, = disabled.
	Watchdog func() *watchdog.Watchdog

	// VisionStore is the per-conversation image attachment store backing the
	// vision-as-tool path. When non-nil, the tool loop rewrites the leading user
	// turn's image blocks to text placeholders and registers them here, so the
	// inspect_image tool (wired to a VisionService over this SAME store) can
	// resolve them. Nil means vision-as-tool is not configured; image blocks pass
	// through and the provider capability gate still strips them for text-only
	// backends. The host and worker each construct one store and hand the SAME
	// instance to both this field and the inspect_image VisionService, so the
	// rewrite and the lookup agree.
	VisionStore agent.VisionAttachStore

	// Profiles is a LIVE accessor for a conversation's active capability profile
	// (the read-only planning fence, and future modes). Like Watchdog it is read
	// at turn time so the host can flip the active profile mid-session via the
	// broker. It is keyed by conversation ID so planning mode is per-conversation
	// and never leaks across attached clients. A nil func, or a func returning a
	// zero Profile, means unrestricted (no fence) — the default posture.
	Profiles func(convID string) agent.Profile

	// RoutingLog records provider/profile/model selection and fallback metadata
	// for postmortems. Nil disables logging. The writer must not receive prompt
	// bodies, tool args, API keys, or response text.
	RoutingLog *routinglog.Writer
}
