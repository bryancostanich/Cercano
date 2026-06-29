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
}

// Engine routes dispatch calls to the appropriate provider.
type Engine struct {
	providers     Providers
	modeFn        func() locus.Mode
	ctxLoader     *projectctx.Loader
	modelFor      func(isCloud bool) string
	usageSink     func(usage.Usage)
	agenticRunner AgenticRunner
}

// NewEngine constructs an Engine. ctx may be nil (project context injection skipped).
// IMPORTANT: pass RAW (unwrapped) providers here. Engine wraps each provider per-dispatch
// via usage.Wrap to label usage by source; passing already-wrapped providers causes double-counting.
func NewEngine(p Providers, modeFn func() locus.Mode, ctx *projectctx.Loader) *Engine {
	return &Engine{
		providers: p,
		modeFn:    modeFn,
		ctxLoader: ctx,
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
	// 1. Select provider via locus.
	sel, err := Select(e.modeFn(), spec.Role, e.providers)
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

	// 5. Call provider, wrapped for source-labeled usage recording.
	prov := usage.Wrap(sel.Provider, spec.Source, sel.IsCloud, e.usageSink)
	resp, err := prov.Chat(ctx, req)
	if err != nil {
		return Result{}, err
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
