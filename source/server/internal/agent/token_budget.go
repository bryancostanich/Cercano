package agent

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// TokenBudget bounds cumulative token volume: provider-reported input/output
// plus explicitly labeled compaction estimates when usage is unavailable. It is
// not a currency/billing meter. It exists for
// delegated sub-agent work: a dispatch grinding through huge-window cloud
// models never trips the per-request context preflight or the iteration cap,
// so cumulative spend was previously unbounded (a single runaway dispatch
// billed millions of tokens; docs/bugs/deepinfra-dispatch-followups.md).
//
// Semantics of TokenBudget values:
//
//	 0 — no budget (unlimited). Main-turn loops use this: a human is present
//	     and the turn is their own spend, not delegated work.
//	>0 — hard cumulative cap; the loop stops with ErrTokenBudgetExhausted
//	     after the response that crosses it.
//
// The check runs AFTER each provider response: the response that crosses the
// budget is kept (its tokens are already billed; discarding it would waste
// them), tool calls from that response are NOT executed, and the loop returns
// partial history and an evidence-based handoff with the classified error.
type TokenBudget struct {
	// Limit caps cumulative token volume (reported plus estimates). 0 disables.
	Limit int
	// Spent is total enforced token volume, including labeled fallback estimates.
	Spent int
	// Estimated is the portion of Spent not reported by a provider.
	Estimated int
}

// Add records reported usage. Unavailable/negative counts add nothing here;
// unreported compaction calls use AddEstimated explicitly.
func (b *TokenBudget) Add(in, out int) {
	if in > 0 {
		b.Spent += in
	}
	if out > 0 {
		b.Spent += out
	}
}

// Exhausted reports whether the budget is enforced and crossed.
func (b *TokenBudget) Exhausted() bool {
	return b.Limit > 0 && b.Spent >= b.Limit
}

// Err builds the classified terminal error for an exhausted budget.
func (b *TokenBudget) Err() error {
	return &llm.Error{
		Class:    llm.ErrTokenBudgetExhausted,
		Provider: "token-budget",
		Used:     b.Spent,
		Limit:    b.Limit,
		Err: fmt.Errorf(
			"dispatch token budget exhausted: %d token-volume units of a %d-token limit (%d provider-reported input+output, including cache reads where reported; %d estimated). This is not a monetary billing total",
			b.Spent, b.Limit, b.Spent-b.Estimated, b.Estimated),
	}
}

// AddEstimated keeps the source visible; it never pretends an estimate is a bill.
func (b *TokenBudget) AddEstimated(tokens int) {
	if tokens > 0 {
		b.Spent += tokens
		b.Estimated += tokens
	}
}

// AddUsage prefers normalized inclusive provider counts (including known zero),
// retaining legacy adapters' reported counters when normalization is absent.
func (b *TokenBudget) AddUsage(u llm.TokenUsage, in, out int) {
	if u.Input.Known {
		in = int(u.Input.Value)
	}
	if u.Output.Known {
		out = int(u.Output.Value)
	}
	b.Add(in, out)
}
