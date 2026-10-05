package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/conversation"
)

// Host-managed algorithmic continuation for durable autonomy runs.
//
// While a conversation has an autonomy run in the ledger whose state is
// "running", the host — not the model, not the client — chains another turn on
// the same conversation after each normal successful turn. Every blocking
// decision is made from structured, durable state:
//
//   - run state "running"            → chain the next turn
//   - review_pending / completed /
//     abandoned / no active run      → stop (human review or finished)
//   - turn superseded (a new user
//     message called BeginTurn)      → stop (user input supersedes)
//   - stream/turn ctx canceled       → stop (no automatic relaunch: the chain
//     only ever rides the client-initiated stream that started it, so a server
//     restart never auto-launches work)
//   - a turn that fails              → stop and propagate the error
//   - an explicit report_autonomous_blocker record (blocker_json) → stop
//     silently (the model's own final response is the user-facing blocker
//     notice); the run stays "running" and the user's next explicit message
//     clears the blocker and resumes the chain
//   - autonomyNoProgressLimit consecutive turns that produce neither durable
//     ledger progress (run identity or state changed) nor real tool work
//     (successful non-bookkeeping tool executions, completed sub-agents) →
//     pause (the run stays "running"; the user can continue it by sending a
//     message)
//
// The model's prose is never parsed: it signals through the autonomy ledger
// tools (capture_decision, request_autonomous_exit, auto_exit, …), which is
// durable and inspectable.

// autonomyNoProgressLimit bounds consecutive continuation turns that produce no
// durable ledger progress, so a wedged run cannot spin the model forever.
const autonomyNoProgressLimit = 3

// autonomyProfileName is the profile entered by request_autonomous_execution's
// approval path; restored at turn start from the durable ledger.
const autonomyProfileName = "autonomous"

// autonomyContinuationMarkerPrefix marks host-generated continuation inputs in
// history and model context, so they are recognizable as host-authored and are
// never mistaken for user prose.
const autonomyContinuationMarkerPrefix = "<host:autonomous-continuation"

// autonomyContinuationInput builds the structured host-authored input for one
// algorithmic continuation turn. It is appended to the conversation as a user
// turn (the runner's history path is user→assistant), so it carries the marker
// to distinguish it from human input.
func autonomyContinuationInput(runID string, turn int) string {
	return fmt.Sprintf(
		"%s run_id=%q turn=%d/>\nContinue the autonomous run: take the next concrete step toward the brief's done-when, record significant decisions with capture_decision, and call request_autonomous_exit once the goal is met. If you are blocked and need the user, call report_autonomous_blocker with a specific reason and stop.",
		autonomyContinuationMarkerPrefix, runID, turn)
}

// autonomyGate is the host's algorithmic continuation decision after a turn.
type autonomyGate struct {
	cont  bool // whether the host chains another turn
	run   conversation.AutonomyRun
	input string // structured continuation input for the next turn
	// notice is a ProgressUpdate text when the host pauses a still-active run
	// for host-side reasons the model's prose could not have announced (the
	// idle-limit safeguard). An explicit report_autonomous_blocker stop sets
	// blocker instead and stays silent: the model's own final response is the
	// user-facing blocker notice (verification, next steps, reason), so host
	// meta would only duplicate it.
	notice  string
	blocker bool // stop caused by an explicit report_autonomous_blocker record
}

// activeAutonomyRun returns the conversation's active ledger run ("running" or
// "review_pending"), ok=false when there is no store, no conversation, or no
// active run.
func (s *Server) activeAutonomyRun(ctx context.Context, convID string) (conversation.AutonomyRun, bool) {
	if convID == "" || s.persistSvc == nil || s.persistSvc.Store() == nil {
		return conversation.AutonomyRun{}, false
	}
	run, err := s.persistSvc.Store().GetActiveAutonomyRun(ctx, convID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("[autonomy] ledger lookup failed for %s: %v", convID, err)
		}
		return conversation.AutonomyRun{}, false
	}
	return run, true
}

// restoreAutonomyProfileAtTurnStart rehydrates the autonomous profile from the
// durable ledger at the start of a client-initiated request. A reconnected or
// restarted session must not depend on the client calling GetSessionProfile
// before its first turn for the capability fence to be in place. Reports whether
// the profile was (or already is) autonomous. This does NOT launch any turn —
// continuation only ever happens inside an open, user-initiated stream.
func (s *Server) restoreAutonomyProfileAtTurnStart(ctx context.Context, convID string) bool {
	if s.profileBroker == nil || convID == "" || s.persistSvc == nil || s.persistSvc.Store() == nil {
		return false
	}
	if active := s.profileBroker.ActiveName(convID); active == autonomyProfileName {
		return true
	} else if active != agent.DefaultProfileName {
		// The session is fenced in some other posture (e.g. planning); never
		// override it from the ledger.
		return false
	}
	run, ok := s.activeAutonomyRun(ctx, convID)
	if !ok {
		return false
	}
	if run.State != "running" && run.State != "review_pending" {
		return false
	}
	if err := s.setSessionProfile(convID, autonomyProfileName); err != nil {
		log.Printf("[autonomy] restore profile for %s failed: %v", convID, err)
		return false
	}
	return true
}

// clearAutonomyBlockerAtRequestStart clears a recorded report_autonomous_blocker
// pause at the start of a client-initiated request: the user's explicit message
// IS the resume signal. The paused run stayed "running" the whole time, so
// clearing the blocker record lets the host chain turns again after this turn.
// Failures are logged and swallowed — a stale blocker at worst keeps the chain
// paused, which is the safe direction.
func (s *Server) clearAutonomyBlockerAtRequestStart(ctx context.Context, convID string) {
	run, ok := s.activeAutonomyRun(ctx, convID)
	if !ok || run.State != "running" {
		return
	}
	if _, blocked := run.ActiveBlocker(); !blocked {
		return
	}
	run.BlockerJSON = ""
	run.UpdatedAt = time.Now()
	if err := s.persistSvc.Store().UpdateAutonomyRun(ctx, run); err != nil {
		log.Printf("[autonomy] clear recorded blocker for %s failed: %v", convID, err)
	}
}

// evaluateAutonomyContinuation decides, from durable structured state plus the
// turn's own tool evidence, whether the host should chain another turn in this
// conversation after a normal successful turn. prev/havePrev is the ledger
// snapshot taken before the turn that just finished; work carries the host-side
// work monitor for this chain segment (see autonomy_work.go); noProgress
// (caller-owned) tracks the consecutive turn count without progress. Never
// parses model prose.
func (s *Server) evaluateAutonomyContinuation(ctx context.Context, convID string, turnGen uint64, turn int, prev conversation.AutonomyRun, havePrev bool, work *autonomyWorkMonitor, noProgress *int) autonomyGate {
	if convID == "" || s.persistSvc == nil || s.persistSvc.Store() == nil {
		return autonomyGate{}
	}
	if ctx.Err() != nil {
		// Stream gone or canceled: stop. The chain only rides the open
		// client-initiated stream — never auto-relaunched on restart.
		return autonomyGate{}
	}
	if !s.turnIsCurrent(convID, turnGen) {
		// Superseded by a newer turn (a new user message called BeginTurn):
		// user input supersedes autonomous continuation.
		return autonomyGate{}
	}
	run, ok := s.activeAutonomyRun(ctx, convID)
	if !ok {
		// No active run: completed/abandoned (or never created). Stop.
		return autonomyGate{}
	}
	// Only a "running" run continues. review_pending waits for human review.
	if run.State != "running" {
		return autonomyGate{run: run}
	}
	// An explicit report_autonomous_blocker record stops the chain. This is
	// the model's own "I need the user" signal: the run deliberately stays
	// "running" (no state mutation, approvals untouched), the blocker carries
	// the required reason, and the user's next explicit message clears it and
	// resumes the chain — nothing auto-restarts it. The stop is SILENT for the
	// user: the model's final response already presented the blocker in its own
	// prose (the blocker protocol requires it), so the host emits no duplicate
	// meta notices — unlike the idle-limit safeguard below, whose host-side
	// cause the model could not have announced.
	if _, blocked := run.ActiveBlocker(); blocked {
		return autonomyGate{run: run, blocker: true}
	}
	// Durable progress = the ledger's content advanced during the turn (the
	// run identity or its state changed; a new run created during the turn
	// counts). Decisions bookkeeping (capture_decision) is excluded — see
	// autonomyLedgerContentChanged. Durable content progress always resets
	// the bound.
	if autonomyLedgerContentChanged(prev, havePrev, run) {
		*noProgress = 0
		return autonomyGate{
			cont:  true,
			run:   run,
			input: autonomyContinuationInput(run.RunID, turn),
		}
	}
	// Where the ledger is quiet, the turn's own evidence decides. Real useful
	// work — successful non-bookkeeping tool executions, completed sub-agents —
	// must reset the bound, otherwise the run would pause every limit turns
	// even while doing genuine file/command work that never touches the
	// ledger. Idle turns (prose only, failing tools, bookkeeping only)
	// increment it. Timestamps alone are never evidence: ledger rows use
	// second-granularity wall clocks that cannot prove a turn did work.
	if work != nil && work.HasWork() {
		*noProgress = 0
		return autonomyGate{
			cont:  true,
			run:   run,
			input: autonomyContinuationInput(run.RunID, turn),
		}
	}
	*noProgress++
	if *noProgress >= autonomyNoProgressLimit {
		// Bounded no-progress safeguard: pause the still-active run with a
		// structured notice. Never mutate the ledger.
		return autonomyGate{run: run, notice: fmt.Sprintf(
			"autonomous continuation paused: %d consecutive turns without ledger progress or real tool work (run %s is still running); send a message to continue",
			*noProgress, run.RunID)}
	}
	return autonomyGate{
		cont:  true,
		run:   run,
		input: autonomyContinuationInput(run.RunID, turn),
	}
}

// autonomyLedgerContentChanged reports whether the ledger's durable content —
// the run identity or its state — changed between the pre-turn snapshot and
// now. Captured-decisions bookkeeping (capture_decision appending to
// DecisionsJSON) is deliberately EXCLUDED: the model may be prompted to call
// capture_decision every turn, so counting decisions as progress would let a
// run reset the no-progress bound indefinitely without doing any work. Genuine
// per-turn progress is measured instead by the work monitor (autonomy_work.go):
// successful non-bookkeeping tool executions and completed sub-agents.
// Timestamps are excluded for the same reason — ledger rows use second-
// granularity wall clocks that cannot prove a turn did work.
func autonomyLedgerContentChanged(prev conversation.AutonomyRun, havePrev bool, cur conversation.AutonomyRun) bool {
	if !havePrev {
		// The run itself was created (or first observed) during the turn.
		return true
	}
	return prev.RunID != cur.RunID || prev.State != cur.State
}

// autonomyContinuationAnnouncement is the structured progress event the host
// emits before each continuation turn, so clients can render a distinct
// "continuing autonomously" marker instead of scraping prose.
func autonomyContinuationAnnouncement(runID string, turn int) string {
	return fmt.Sprintf("autonomous continuation: continuing run %s (turn %d)", runID, turn)
}
