package builtins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
)

type suggestAutonomousArgs struct {
	Reason         string   `json:"reason"`
	Goal           string   `json:"goal"`
	DoneWhen       []string `json:"done_when"`
	Constraints    []string `json:"constraints"`
	ReviewPoints   []string `json:"review_points"`
	SourcePlanPath string   `json:"source_plan_path"`
	SourceSpecPath string   `json:"source_spec_path"`
}

type suggestAutonomousCap struct{}

// SuggestAutonomous proposes entering autonomous mode with a lightweight run
// brief. It is X-tier so the standard y/n/d/c confirmation gate is the approval
// boundary before Execute flips the profile.
func SuggestAutonomous() capabilities.Capability { return suggestAutonomousCap{} }

func (suggestAutonomousCap) Name() string                   { return "suggest_autonomous" }
func (suggestAutonomousCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (suggestAutonomousCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (suggestAutonomousCap) Description() string {
	return "Propose starting autonomous mode with a lightweight run brief. Draft concise goal, done_when, constraints, and review_points fields first; the user is shown a y/n/d/c prompt and autonomous mode starts only if approved. Use this for hands-off execution after a direct user request or after an accepted plan."
}
func (suggestAutonomousCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"reason":{"type":"string","description":"Short reason autonomous mode is appropriate."},
			"goal":{"type":"string","description":"One concise goal for the autonomous run."},
			"done_when":{"type":"array","items":{"type":"string"},"description":"Short checklist of completion criteria."},
			"constraints":{"type":"array","items":{"type":"string"},"description":"Boundaries the agent must honor."},
			"review_points":{"type":"array","items":{"type":"string"},"description":"Decision or risk areas to track during execution; escalate unresolved issues when they arise."},
			"source_plan_path":{"type":"string","description":"Optional plan.md path when deriving from planning mode."},
			"source_spec_path":{"type":"string","description":"Optional spec.md path when deriving from planning mode."}
		},
		"required":["goal"]
	}`)
}
func (suggestAutonomousCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a suggestAutonomousArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("suggest_autonomous: parse args: %w", err)
		}
	}
	msg, err := enterAutonomousMode(ctx, call, autonomousEntryRequest{
		Reason:         a.Reason,
		Goal:           a.Goal,
		DoneWhen:       a.DoneWhen,
		Constraints:    a.Constraints,
		ReviewPoints:   a.ReviewPoints,
		SourcePlanPath: a.SourcePlanPath,
		SourceSpecPath: a.SourceSpecPath,
		ErrPrefix:      "suggest_autonomous",
	})
	if err != nil {
		return nil, err
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: msg}, nil
}

type autonomousEntryRequest struct {
	Reason         string
	Goal           string
	DoneWhen       []string
	Constraints    []string
	ReviewPoints   []string
	SourcePlanPath string
	SourceSpecPath string
	ErrPrefix      string
}

func enterAutonomousMode(ctx context.Context, call *capabilities.Call, req autonomousEntryRequest) (string, error) {
	prefix := strings.TrimSpace(req.ErrPrefix)
	if prefix == "" {
		prefix = "autonomous"
	}
	if strings.TrimSpace(req.Goal) == "" {
		return "", fmt.Errorf("%s: goal is required", prefix)
	}
	store, convID, err := requireAutonomyStore(call, prefix)
	if err != nil {
		return "", err
	}
	if active, err := store.GetActiveAutonomyRun(ctx, convID); err == nil {
		return "", fmt.Errorf("%s: autonomous run already active for conversation %s (run %s is %s)", prefix, convID, active.RunID, active.State)
	} else if !isNoRows(err) {
		return "", fmt.Errorf("%s: check active autonomy run: %w", prefix, err)
	}

	brief := conversation.AutonomyBrief{
		Goal:         strings.TrimSpace(req.Goal),
		DoneWhen:     compactStrings(req.DoneWhen),
		Constraints:  compactStrings(req.Constraints),
		ReviewPoints: compactStrings(req.ReviewPoints),
	}
	briefJSON, err := json.Marshal(brief)
	if err != nil {
		return "", fmt.Errorf("%s: marshal brief: %w", prefix, err)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "initial autonomous brief"
	}
	revsJSON, err := json.Marshal([]conversation.AutonomyBriefRevision{{
		Number:    1,
		Actor:     "assistant",
		Reason:    reason,
		Timestamp: time.Now(),
		Brief:     brief,
	}})
	if err != nil {
		return "", fmt.Errorf("%s: marshal brief revisions: %w", prefix, err)
	}
	sourceKind := "direct_user_request"
	if strings.TrimSpace(req.SourcePlanPath) != "" || strings.TrimSpace(req.SourceSpecPath) != "" {
		sourceKind = "accepted_plan"
	}
	run, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{
		ConversationID: convID,
		State:          "running",
		SourceKind:     sourceKind,
		SourcePlanPath: strings.TrimSpace(req.SourcePlanPath),
		SourceSpecPath: strings.TrimSpace(req.SourceSpecPath),
		BriefJSON:      string(briefJSON),
		RevisionsJSON:  string(revsJSON),
		DecisionsJSON:  "[]",
		ReviewJSON:     "{}",
	})
	if err != nil {
		return "", fmt.Errorf("%s: create autonomy run: %w", prefix, err)
	}
	if call.Svc.EnterProfile == nil {
		markAutonomyRunAbandoned(ctx, store, run)
		return "", fmt.Errorf("%s: autonomous mode is not available (no profile broker wired)", prefix)
	}
	if err := call.Svc.EnterProfile(convID, "autonomous"); err != nil {
		markAutonomyRunAbandoned(ctx, store, run)
		return "", fmt.Errorf("%s: entering autonomous mode: %w", prefix, err)
	}
	msg := "Entered autonomous mode. Work to the approved run brief, capture meaningful in-scope decisions, continue unless a high-risk boundary is crossed, and request autonomous exit when the brief is satisfied."
	if r := strings.TrimSpace(req.Reason); r != "" {
		msg = "Entered autonomous mode: " + r + ".\n\n" + msg
	}
	return msg, nil
}

type autoExitCap struct{}

// AutoExit leaves autonomous mode without declaring the run complete.
func AutoExit() capabilities.Capability { return autoExitCap{} }

func (autoExitCap) Name() string                   { return "auto_exit" }
func (autoExitCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (autoExitCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (autoExitCap) Description() string {
	return "Exit autonomous mode without marking the autonomous run complete. Use this when abandoning or pausing the autonomous protocol; the user is shown a y/n/d/c prompt before the mode is left."
}
func (autoExitCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{"reason":{"type":"string","description":"Short note on why autonomous mode is being left."}}
	}`)
}
func (autoExitCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a struct {
		Reason string `json:"reason"`
	}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("auto_exit: parse args: %w", err)
		}
	}
	store, run, err := requireActiveAutonomyRun(ctx, call, "auto_exit", "running", "review_pending")
	if err != nil {
		return nil, err
	}
	run.State = "abandoned"
	run.UpdatedAt = time.Now()
	if err := store.UpdateAutonomyRun(ctx, run); err != nil {
		return nil, fmt.Errorf("auto_exit: update autonomy ledger: %w", err)
	}
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("auto_exit: autonomous mode is not available (no profile broker wired)")
	}
	if err := call.Svc.EnterProfile(call.ConversationID, "default"); err != nil {
		return nil, fmt.Errorf("auto_exit: leaving autonomous mode: %w", err)
	}
	msg := "Exited autonomous mode."
	if r := strings.TrimSpace(a.Reason); r != "" {
		msg = "Exited autonomous mode: " + r
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: msg}, nil
}

type requestAutonomousExitCap struct{}

// RequestAutonomousExit completes an approved run and leaves autonomous mode.
// Permission gating happens before Execute, so declining leaves the run untouched.
func RequestAutonomousExit() capabilities.Capability { return requestAutonomousExitCap{} }

func (requestAutonomousExitCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (requestAutonomousExitCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (requestAutonomousExitCap) Name() string { return "request_autonomous_exit" }
func (requestAutonomousExitCap) Description() string {
	return "Complete the autonomous run and exit autonomous mode through one confirmation. Before calling, present results, verification, and remaining limitations. Approval marks the run completed and leaves autonomous mode; rejection leaves it active. Captured decisions are an audit trail: do not replay them or ask for renewed acceptance. Raise unresolved blockers and new high-risk choices when they arise, not at completion."
}
func (requestAutonomousExitCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object","properties":{
			"summary":{"type":"string","description":"Completed work and any remaining limitations."},
			"verification":{"type":"string","description":"Checks performed and their results."}
		},"required":["summary","verification"]}`)
}
func (requestAutonomousExitCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var args struct {
		Summary      string `json:"summary"`
		Verification string `json:"verification"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("request_autonomous_exit: parse args: %w", err)
	}
	if strings.TrimSpace(args.Summary) == "" || strings.TrimSpace(args.Verification) == "" {
		return nil, fmt.Errorf("request_autonomous_exit: summary and verification are required")
	}
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("request_autonomous_exit: autonomous mode is not available (no profile broker wired)")
	}
	// review_pending is accepted only to recover runs saved by older versions.
	store, run, err := requireActiveAutonomyRun(ctx, call, "request_autonomous_exit", "running", "review_pending")
	if err != nil {
		return nil, err
	}
	previous := run
	review := map[string]any{}
	if strings.TrimSpace(run.ReviewJSON) != "" {
		if err := json.Unmarshal([]byte(run.ReviewJSON), &review); err != nil {
			return nil, fmt.Errorf("request_autonomous_exit: decode completion record: %w", err)
		}
	}
	if review == nil {
		review = map[string]any{}
	}
	review["summary"] = args.Summary
	review["verification"] = args.Verification
	review["completed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(review)
	if err != nil {
		return nil, err
	}
	run.State = "completed"
	run.UpdatedAt = time.Now()
	run.ReviewJSON = string(data)
	if err := store.UpdateAutonomyRun(ctx, run); err != nil {
		return nil, fmt.Errorf("request_autonomous_exit: save completion: %w", err)
	}
	if err := call.Svc.EnterProfile(call.ConversationID, "default"); err != nil {
		// Restore the active record so a failed profile transition can be retried.
		if rollbackErr := store.UpdateAutonomyRun(context.WithoutCancel(ctx), previous); rollbackErr != nil {
			return nil, fmt.Errorf("request_autonomous_exit: leaving autonomous mode: %v; restoring active ledger failed: %w", err, rollbackErr)
		}
		return nil, fmt.Errorf("request_autonomous_exit: leaving autonomous mode: %w", err)
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: "Autonomous run completed. Left autonomous mode."}, nil
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func requireAutonomyStore(call *capabilities.Call, prefix string) (conversation.Store, string, error) {
	if call == nil {
		return nil, "", fmt.Errorf("%s: capability call is required", prefix)
	}
	if call.Svc.Conversations == nil {
		return nil, "", fmt.Errorf("%s: autonomy ledger is not available", prefix)
	}
	convID := strings.TrimSpace(call.ConversationID)
	if convID == "" {
		return nil, "", fmt.Errorf("%s: conversation id is required", prefix)
	}
	return call.Svc.Conversations, convID, nil
}

func requireActiveAutonomyRun(ctx context.Context, call *capabilities.Call, prefix string, states ...string) (conversation.Store, conversation.AutonomyRun, error) {
	store, convID, err := requireAutonomyStore(call, prefix)
	if err != nil {
		return nil, conversation.AutonomyRun{}, err
	}
	run, err := store.GetActiveAutonomyRun(ctx, convID)
	if err != nil {
		if isNoRows(err) {
			return nil, conversation.AutonomyRun{}, fmt.Errorf("%s: no active autonomous run for conversation %s", prefix, convID)
		}
		return nil, conversation.AutonomyRun{}, fmt.Errorf("%s: load active autonomy run: %w", prefix, err)
	}
	if !autonomyStateAllowed(run.State, states...) {
		return nil, conversation.AutonomyRun{}, fmt.Errorf("%s: active autonomous run %s is %s; want %s", prefix, run.RunID, run.State, strings.Join(states, " or "))
	}
	return store, run, nil
}

func autonomyStateAllowed(state string, states ...string) bool {
	for _, want := range states {
		if state == want {
			return true
		}
	}
	return false
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func markAutonomyRunAbandoned(ctx context.Context, store conversation.Store, run conversation.AutonomyRun) {
	run.State = "abandoned"
	run.UpdatedAt = time.Now()
	_ = store.UpdateAutonomyRun(ctx, run)
}

func decodeAutonomyDecisions(raw string) ([]conversation.AutonomyDecision, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var decisions []conversation.AutonomyDecision
	if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
		return nil, err
	}
	return decisions, nil
}
