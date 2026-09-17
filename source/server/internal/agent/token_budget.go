package agent

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// TokenBudget bounds the cumulative provider-reported tokens (input + output,
// summed across every model call) one tool loop may bill. It exists for
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
// partial history plus the classified error so the caller can re-dispatch
// with tighter scope.
type TokenBudget struct {
	// Limit is the cumulative input+output token cap. 0 disables enforcement.
	Limit int
	// Spent is the running provider-reported total.
	Spent int
}

// Add records one provider response's reported usage. Estimated/zero counts
// add nothing: enforcement is deliberately based only on what the provider
// says it billed, so a provider that reports no usage is never cut off by
// guesswork (the iteration cap remains that path's backstop).
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
			"dispatch token budget exhausted: ~%d tokens billed of a %d-token budget; narrow the task, grant fewer files/tools, or split the work into smaller dispatches",
			b.Spent, b.Limit),
	}
}
