package dispatch

import (
	"context"
	"errors"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/usage"
)

// Mode selects the dispatch execution model.
type Mode int

const (
	OneShot Mode = iota // single routed llm.Provider.Chat call
	Agentic             // bounded tool loop (not yet implemented)
)

// Spec describes a unit of work to dispatch.
type Spec struct {
	Mode                Mode
	Role                Role
	Prompt              string
	System              string
	WantsProjectContext bool
	WorkDir             string
	ConversationID      string
	Source              string
	ModelOverride       string // advisory model name within locus bounds

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

	// MaxIterations caps the number of LLM round-trips in the loop.
	// 0 means use the package default (agent.MaxToolLoopIterations = 50).
	MaxIterations int
}

// Result holds the outcome of a dispatched call.
type Result struct {
	Text         string
	Model        string
	IsCloud      bool
	Notice       string
	InputTokens  int
	OutputTokens int

	// SubConversationID names the persisted conversation holding the
	// sub-agent's full tool loop (agentic dispatches with a store wired).
	// Empty when persistence is unavailable.
	SubConversationID string
}

// Engine routes dispatch calls to the appropriate provider.
type Engine struct {
	providersFn   func() Providers
	modeFn        func() locus.Mode
	ctxLoader     *projectctx.Loader
	modelFor      func(isCloud bool) string
	usageSink     func(usage.Usage)
	agenticRunner AgenticRunner
}

// NewEngine constructs an Engine. ctx may be nil (project context injection skipped).
//
// providersFn is called per dispatch to resolve the current candidate providers,
// so a runtime cloud-provider swap (e.g. a cloud-profile change) is honored
// without rebuilding the engine. IMPORTANT: it must return RAW (unwrapped)
// providers — the engine emits usage directly and conditionally (controlled by
// Spec.RecordUsage) so returning already-wrapped providers would double-count.
func NewEngine(providersFn func() Providers, modeFn func() locus.Mode, ctx *projectctx.Loader) *Engine {
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

// SetModelFor installs a function that maps provider tier to a model name string.
func (e *Engine) SetModelFor(fn func(isCloud bool) string) {
	e.modelFor = fn
}

// Dispatch executes spec and returns a Result.
func (e *Engine) Dispatch(ctx context.Context, spec Spec) (Result, error) {
	// 1. Select provider via locus (providers resolved fresh each dispatch).
	sel, err := Select(e.modeFn(), spec.Role, e.providersFn())
	if err != nil {
		return Result{}, err
	}

	// 2. Resolve model name.
	model := ""
	if e.modelFor != nil {
		model = e.modelFor(sel.IsCloud)
	}
	if spec.ModelOverride != "" {
		model = spec.ModelOverride
	}

	// 3a. Agentic: delegate to the installed runner (lives in internal/server
	// to avoid an import cycle with internal/agent).
	if spec.Mode == Agentic {
		if e.agenticRunner == nil {
			return Result{}, errors.New("dispatch: agentic runner not configured")
		}
		return e.agenticRunner(ctx, spec, sel, model)
	}

	// 3b. Optionally prepend project context (OneShot only).
	prompt := spec.Prompt
	if spec.WantsProjectContext && spec.WorkDir != "" && e.ctxLoader != nil {
		prompt = e.ctxLoader.PrependContext(spec.WorkDir, prompt)
	}

	// 4. Build chat request.
	req := llm.ChatRequest{
		Model:  model,
		System: spec.System,
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
	if err != nil {
		return Result{}, err
	}
	if spec.RecordUsage && e.usageSink != nil {
		e.usageSink(usage.Usage{
			Source:               spec.Source,
			Model:                model,
			IsCloud:              sel.IsCloud,
			InputTokens:          resp.InputTokens,
			OutputTokens:         resp.OutputTokens,
			ContentTokensAvoided: spec.ContentTokensAvoided,
			TokenSaving:          true,
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
		Text:         text,
		Model:        model,
		IsCloud:      sel.IsCloud,
		Notice:       sel.Notice,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}
