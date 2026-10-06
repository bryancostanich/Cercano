package server

import (
	"cercano/source/server/internal/runner"
)

// Host-side evidence that a turn did real, useful work even when the durable
// autonomy ledger was not touched. The no-progress bound (see
// autonomy_continuation.go) exists to catch a run that spins in place; it must
// not pause a run that is genuinely working just because its work lands in
// files, commands, or sub-agents instead of ledger rows. Evidence is derived
// from the runner events the initiator stream already drains per turn, so
// there is no schema change and no wall-clock timing involved.

// autonomyBookkeepingTools lists tool names whose successful execution is
// autonomy/plan bookkeeping or session control rather than task work. The list
// is deliberately narrow and curated: every other successful tool result —
// including MCP and future tools — counts as work, so a run cannot be paused
// for doing something this safeguard did not anticipate. It is also the
// anti-gaming half of the safeguard: bookkeeping alone (notably
// capture_decision, which the model may be prompted to call every turn) can
// never reset the no-progress bound.
var autonomyBookkeepingTools = map[string]struct{}{
	"capture_decision":             {}, // durable ledger bookkeeping
	"request_autonomous_execution": {}, // run entry
	"suggest_autonomous":           {}, // run suggestion
	"request_autonomous_exit":      {}, // run exit request
	"auto_exit":                    {}, // run exit
	"report_autonomous_blocker":   {}, // explicit pause request, not work
	"plan_set_status":              {}, // plan bookkeeping
	"plan_exit":                    {}, // plan bookkeeping
	"suggest_plan":                 {}, // plan bookkeeping
	"request_plan_approval":        {}, // approval request, not work
	"session_model":                {}, // session control
	"get_protocol":                 {}, // protocol read-back, not work
	"restart_agent":                {}, // runtime control
	"restart_runtime":              {}, // runtime control
}

// isAutonomyWorkTool reports whether a successful execution of name counts as
// work. Unknown names count; curated bookkeeping does not.
func isAutonomyWorkTool(name string) bool {
	_, bookkeeping := autonomyBookkeepingTools[name]
	return !bookkeeping
}

// isAutonomySubAgentDone reports whether a sub-agent progress event marks a
// sub-agent (or activity) run that actually finished. Start/prompt/token
// events carry no completion evidence; error events are excluded so a
// sub-agent that fails repeatedly cannot keep the chain alive.
func isAutonomySubAgentDone(kind string) bool {
	return kind == "done"
}

// autonomyWorkMonitor counts work evidence in one chain segment (the turns
// since the gate last chained a new turn). It is not safe for concurrent use;
// the initiator loop drains turn events on a single goroutine.
type autonomyWorkMonitor struct {
	// count tallies successful work-tool executions since the last reset. The
	// host uses it as a reset condition for the no-progress bound, so events
	// arriving after the segment's evidence was consumed (reset) must be
	// ignored: they would silently revive an exhausted streak and unbound the
	// chain.
	count  int
	closed bool
}

func newAutonomyWorkMonitor() *autonomyWorkMonitor { return &autonomyWorkMonitor{} }

// Observe classifies one runner event from the turn event stream. Events
// observed after Reset are ignored: the segment's evidence was already
// consumed for a decision, and late events must not resurrect it.
func (m *autonomyWorkMonitor) Observe(ev runner.Event) {
	if m == nil || m.closed {
		return
	}
	switch ev.Kind {
	case runner.EventToolExecComplete:
		// A repeated failing tool must not reset the bound: only successful
		// executions count.
		if !ev.IsError && isAutonomyWorkTool(ev.ToolName) {
			m.count++
		}
	case runner.EventSubAgent:
		// A sub-agent that ran to completion is the strongest work signal.
		if !ev.IsError && isAutonomySubAgentDone(ev.SubAgentKind) {
			m.count++
		}
	}
}

// HasWork reports whether any work evidence was observed since the last Reset.
func (m *autonomyWorkMonitor) HasWork() bool {
	return m != nil && m.count > 0
}

// Reset clears the count and closes the monitor against further Observe
// calls. Called when the gate chains a new turn so the next turn is measured
// on its own evidence; the caller replaces this monitor with a fresh one for
// the next chain segment (see server.go's turn loop).
func (m *autonomyWorkMonitor) Reset() {
	if m == nil {
		return
	}
	m.count = 0
	m.closed = true
}

// autonomyWorkNotice describes the work that reset the no-progress bound, for
// the structured progress note the gate emits when chaining. Keeping the
// wording deterministic keeps the tests timing-free.
func autonomyWorkNotice() string {
	return "no ledger progress but the turn did real tool work; no-progress bound reset"
}
