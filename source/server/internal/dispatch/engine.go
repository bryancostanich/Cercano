package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"cercano/source/server/internal/agenttools"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/internal/usage"
	"cercano/source/server/pkg/config"
)

// newDispatchID mints a short random id for scoping a dispatch's provider
// session identity.
func newDispatchID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Mode selects the dispatch execution model.
type Mode int

const (
	OneShot Mode = iota // single routed inference.Provider.Chat call
	Agentic             // bounded tool loop (not yet implemented)
)

// Spec describes a unit of work to dispatch.
type Spec struct {
	// LocalOffload explicitly retains the local tool's pre-existing placement
	// policy. Ordinary missing-class dispatch never enters this path.
	LocalOffload        bool
	Mode                Mode
	Role                Role
	RoutingTask         config.Task // explicit task routing; empty preserves legacy role policy
	Prompt              string
	System              string
	WantsProjectContext bool
	WorkDir             string
	ConversationID      string
	Source              string
	ModelOverride       string // advisory model name within locus bounds
	// DisableThinking is a one-shot generation policy, not a global setting.
	DisableThinking bool

	// Tier names the model-taxonomy tier this dispatch runs on. Empty
	// defaults by role: RoleMain → everyday, RoleCoproc → fast_light_text.
	// ModelOverride, when set, wins over tier resolution.
	Tier config.Tier

	// ContentTokensAvoided is the estimated cloud tokens saved by handling this
	// OneShot locally; recorded in telemetry when RecordUsage is set.
	ContentTokensAvoided int
	// RecordUsage, when true, emits one usage event (with the savings metric) to
	// the engine's usage sink for this dispatch. Co-processor capabilities set it;
	// processCoproc leaves it false so its telemetry stays MCP-side (no double-count).
	RecordUsage bool

	// Agentic-only fields (ignored for OneShot).

	// Task is the open-ended instruction for the agentic tool loop.
	Task string

	// Tools is the capability/tool name allowlist for agentic dispatch.
	// Empty means: grant R-tier tools only (least-privilege default).
	Tools []string

	// Interactive, when true, signals that a human is watching the loop
	// and event progress should be forwarded (reserved for future use).
	Interactive bool

	// Emit receives structured progress from an agentic dispatch. It is optional
	// and proto-free so dispatch stays independent of runner/proto event types.
	Emit func(agenttools.ProgressEvent)

	// MaxIterations caps the number of LLM round-trips in the loop.
	// 0 means use the package default (agent.MaxToolLoopIterations = 50).
	MaxIterations int
	FallbackTier  config.Tier // per-invocation override fallback intent
}

// Result holds the outcome of a dispatched call.
type Result struct {
	Text         string
	Model        string
	Provider     string
	Tier         string
	IsCloud      bool
	Notice       string
	InputTokens  int
	OutputTokens int

	// SubConversationID names the persisted conversation holding the
	// sub-agent's full tool loop (agentic dispatches with a store wired).
	// Empty when persistence is unavailable.
	SubConversationID string

	// GrantedTools / IgnoredTools report the sub-agent's actual toolset
	// (agentic dispatches): what was granted, and which requested names were
	// ignored as unknown. Surfaced to the caller so a mis-granted sub-agent
	// is visible immediately instead of failing its task quietly.
	GrantedTools []string
	IgnoredTools []string

	// Suspicious flags a likely no-op sub-agent run: the loop reported
	// completion but the tool-use record contradicts the claim. The canonical
	// case is a sub-agent granted a write/execute tool (PermW/PermX) that
	// called none of them yet returned a non-empty "done" summary — a
	// migration or fix cannot have happened without a write or exec call, so
	// the summary is untrustworthy. Agentic dispatch returns this case as an
	// error so the parent cannot treat fabricated write/execute completion as
	// success; the fields remain populated for diagnostics. SuspicionReason is
	// a human-readable explanation.
	Suspicious           bool
	SuspicionReason      string
	Profile, Destination string
	ContextWindow        int
	ContextWindowKnown   bool
}

// Engine routes dispatch calls to the appropriate provider.
type Engine struct {
	providersFn         func() inference.Tiers
	modeFn              func() locus.Mode
	ctxLoader           *projectctx.Loader
	modelFor            func(isCloud bool, tier config.Tier) string
	taskAssignment      func(config.Task) config.TaskAssignment
	destinationModelFor func(inference.Selection, config.Tier) string
	usageSink           func(usage.Usage)
	agenticRunner       AgenticRunner
}

// NewEngine constructs an Engine. ctx may be nil (project context injection skipped).
//
// providersFn is called per dispatch to resolve the current candidate providers,
// so a runtime cloud-provider swap (e.g. a cloud-profile change) is honored
// without rebuilding the engine. IMPORTANT: it must return RAW (unwrapped)
// providers — the engine emits usage directly and conditionally (controlled by
// Spec.RecordUsage) so returning already-wrapped providers would double-count.
func NewEngine(providersFn func() inference.Tiers, modeFn func() locus.Mode, ctx *projectctx.Loader) *Engine {
	return &Engine{
		providersFn: providersFn,
		modeFn:      modeFn,
		ctxLoader:   ctx,
	}
}

// SetUsageSink installs a sink that receives one Usage per completed Chat call,
// labeled by Spec.Source. A nil sink disables recording (safe).
func (e *Engine) SetUsageSink(fn func(usage.Usage)) {
	e.usageSink = fn
}

// SetModelFor installs a function that resolves the model for a dispatch:
// the already-selected provider side plus the taxonomy tier the work runs on.
func (e *Engine) SetModelFor(fn func(isCloud bool, tier config.Tier) string) {
	e.modelFor = fn
}

// Target resolves the concrete provider/model metadata a dispatch would use
// without sending a prompt. It mirrors Dispatch's selection and model-resolution
// rules so callers that must budget before constructing a prompt can stay in
// sync with execution.
func (e *Engine) Target(spec Spec) (modelbudget.Target, error) {
	candidates, mode := e.routingSnapshot()
	sel, tier, model, err := e.resolve(spec, mode, candidates)
	if err != nil {
		return modelbudget.Target{}, err
	}
	return dispatchTarget(context.Background(), sel, tier, model, spec), nil
}
func dispatchTarget(ctx context.Context, sel inference.Selection, tier config.Tier, model string, spec Spec) modelbudget.Target {
	intent := tier
	if spec.ModelOverride != "" {
		tier = ""
	}
	route := inference.TargetForContext(ctx, sel.Provider, inference.Call{Model: model, Tier: string(tier), FallbackTier: string(intent)})
	if route.Profile == "" {
		route.Profile = sel.Profile
	}
	if route.Destination == "" {
		route.Destination = string(sel.Destination)
	}
	return modelbudget.Target{Provider: route.Provider, Profile: route.Profile, Destination: route.Destination, ContextWindow: route.ContextWindow, ContextWindowKnown: route.ContextWindowKnown, Model: route.Model, Tier: string(tier), IsCloud: sel.IsCloud}
}

// PreparedTarget uses the same destination snapshot as dispatch, then prepares
// runtime-confirmed Local capacity before the caller builds its prompt.
func (e *Engine) PreparedTarget(ctx context.Context, spec Spec) (modelbudget.Target, error) {
	candidates, mode := e.routingSnapshot()
	sel, tier, model, err := e.resolve(spec, mode, candidates)
	if err != nil {
		return modelbudget.Target{}, err
	}
	target := dispatchTarget(ctx, sel, tier, model, spec)
	capacity, err := llm.ResolveRuntimeContext(ctx, sel.Provider, target.Model, true)
	if err != nil {
		return target, err
	}
	if capacity.Window > 0 {
		target.ContextWindow = capacity.Window
		target.ContextWindowKnown = true
	}
	return target, nil
}

// Dispatch executes spec and returns a Result.
func (e *Engine) Dispatch(ctx context.Context, spec Spec) (Result, error) {
	spec = defaultTask(spec)
	// 1. Select provider via locus (providers resolved fresh each dispatch).
	candidates, mode := e.routingSnapshot()
	sel, tier, model, err := e.resolve(spec, mode, candidates)
	if err != nil {
		return Result{}, err
	}
	spec.Tier = tier
	if spec.ModelOverride != "" {
		spec.FallbackTier = tier
		spec.Tier = ""
	}

	sel = e.startupFallback(sel, candidates, mode, spec, tier)

	// 3a. Agentic: delegate to the installed runner (lives in internal/server
	// to avoid an import cycle with internal/agent).
	if spec.Mode == Agentic {
		if e.agenticRunner == nil {
			return Result{}, errors.New("dispatch: agentic runner not configured")
		}
		result, err := e.agenticRunner(ctx, spec, sel, model)
		current, servedModel := CurrentRoute(sel, model)
		if current.IsCloud != sel.IsCloud {
			result.Provider = providerName(current)
			result.IsCloud = current.IsCloud
			result.Model = servedModel
			result.Notice = current.Notice
		}
		return result, err
	}

	// 3b. A one-shot is not part of the calling conversation: give it its own
	// provider session identity so its anomaly attribution and any per-session
	// tagging stay disjoint from the caller's conversation.
	ctx = llm.WithSessionID(ctx, "oneshot-"+newDispatchID())

	capacity, prepErr := llm.ResolveRuntimeContext(ctx, sel.Provider, model, true)
	if prepErr != nil {
		return Result{}, prepErr
	}
	ctx = llm.WithRuntimeContext(ctx, capacity)
	// Optionally prepend project context (OneShot only).
	prompt := spec.Prompt
	if spec.WantsProjectContext && spec.WorkDir != "" && e.ctxLoader != nil {
		prompt = e.ctxLoader.PrependContext(spec.WorkDir, prompt)
	}

	// 4. Build chat request.
	req := llm.ChatRequest{
		Model:           model,
		Tier:            string(spec.Tier),
		FallbackTier:    string(spec.FallbackTier),
		System:          spec.System,
		DisableThinking: spec.DisableThinking,
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Blocks: []llm.Block{
					{Type: llm.BlockText, Text: prompt},
				},
			},
		},
	}

	// 5. Call provider directly; emit usage only when the caller opts in.
	resp, err := sel.Provider.Chat(ctx, req)
	sel, model = CurrentRoute(sel, model)
	if err != nil {
		return Result{}, err
	}
	servedModel := model
	servedProvider := providerName(sel)
	if resp.Model != "" {
		servedModel = resp.Model
	}
	route := llm.ServingRoute{Provider: servedProvider, Model: servedModel, Profile: sel.Profile, Destination: string(sel.Destination)}
	if resp.Route != nil {
		route = *resp.Route
		servedProvider = route.Provider
	}
	if spec.RecordUsage && e.usageSink != nil {
		e.usageSink(usage.Usage{
			Source: spec.Source,
			Model:  servedModel, Provider: servedProvider,
			IsCloud:              sel.IsCloud,
			InputTokens:          resp.InputTokens,
			OutputTokens:         resp.OutputTokens,
			ContentTokensAvoided: avoidedTokens(spec.ContentTokensAvoided, sel.IsCloud),
			TokenSaving:          !sel.IsCloud,
		})
	}

	// 6. Collect text blocks.
	var text string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}

	// 7. Return result.
	return Result{
		Text:     text,
		Model:    servedModel,
		Provider: servedProvider, Profile: route.Profile, Destination: route.Destination, ContextWindow: route.ContextWindow, ContextWindowKnown: route.ContextWindowKnown,
		Tier:         string(spec.Tier),
		IsCloud:      sel.IsCloud,
		Notice:       sel.Notice,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}

func providerName(sel inference.Selection) string {
	if sel.Provider == nil {
		return ""
	}
	return sel.Provider.Name()
}

func avoidedTokens(tokens int, isCloud bool) int {
	if isCloud {
		return 0
	}
	return tokens
}

func (e *Engine) SetTaskAssignment(fn func(config.Task) config.TaskAssignment) { e.taskAssignment = fn }
func (e *Engine) SetDestinationModelFor(fn func(inference.Selection, config.Tier) string) {
	e.destinationModelFor = fn
}

func (e *Engine) resolve(spec Spec, mode locus.Mode, candidates inference.Tiers) (inference.Selection, config.Tier, string, error) {
	if spec.LocalOffload && spec.RoutingTask != "" {
		return inference.Selection{}, "", "", fmt.Errorf("dispatch: local offload cannot carry a task assignment")
	}
	spec = defaultTask(spec)
	var sel inference.Selection
	var err error
	tier := spec.Tier
	if spec.RoutingTask != "" {
		// Validate identity before injectable assignment callbacks can give an
		// unknown class valid routing intent. All three entry points share this guard.
		if !config.ValidTask(spec.RoutingTask) {
			return sel, tier, "", fmt.Errorf("dispatch: unknown routing task %q", spec.RoutingTask)
		}
		assignment := (config.Config{}).TaskAssignment(spec.RoutingTask)
		if candidates.TaskFor != nil {
			assignment = candidates.TaskFor(spec.RoutingTask)
		} else if e.taskAssignment != nil {
			assignment = e.taskAssignment(spec.RoutingTask)
		}
		if tier == "" {
			tier = assignment.Quality.CapabilityTier()
		}
		sel, err = inference.SelectDestination(mode, assignment.Destination, candidates)
	} else {
		sel, err = inference.Select(mode, spec.Role, candidates)
		if tier == "" {
			tier = config.TierEveryday
			if spec.Role == RoleCoproc {
				tier = config.TierFastLightText
			}
		}
	}
	if err != nil {
		return sel, tier, "", err
	}
	model := ""
	if spec.RoutingTask != "" && candidates.ModelFor != nil {
		model = candidates.ModelFor(sel, tier)
	} else if spec.RoutingTask != "" && e.destinationModelFor != nil {
		model = e.destinationModelFor(sel, tier)
	} else if spec.RoutingTask != "" && sel.Destination == config.DestinationSecondary {
		return sel, tier, "", errors.New("dispatch: Secondary model resolver unavailable")
	} else if e.modelFor != nil {
		model = e.modelFor(sel.IsCloud, tier)
	}
	if spec.ModelOverride != "" {
		model = spec.ModelOverride
	}
	if spec.RoutingTask != "" {
		request := inference.Call{Model: model, Tier: string(tier)}
		if spec.ModelOverride != "" {
			request.Tier = ""
			request.FallbackTier = string(tier)
		}
		target := inference.TargetForCall(sel.Provider, request)
		model = target.Model
		if target.Profile != "" {
			sel.Profile = target.Profile
		}
	}
	if spec.RoutingTask != "" && model == "" {
		return sel, tier, "", errors.New("dispatch: selected task model unavailable")
	}
	return sel, tier, model, nil
}

func (s Spec) EffectiveTier() config.Tier {
	if s.Tier != "" {
		return s.Tier
	}
	return s.FallbackTier
}

func defaultTask(spec Spec) Spec {
	if spec.RoutingTask == "" && !spec.LocalOffload {
		spec.RoutingTask = config.TaskDispatch
	}
	return spec
}

func (e *Engine) routingSnapshot() (inference.Tiers, locus.Mode) {
	candidates := e.providersFn()
	if candidates.Mode != "" {
		return candidates, candidates.Mode
	}
	return candidates, e.modeFn()
}
