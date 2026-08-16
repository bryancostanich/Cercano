package builtins

import (
	"context"
	"encoding/json"
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
			"review_points":{"type":"array","items":{"type":"string"},"description":"Decision or risk areas to capture for final review."},
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
	if strings.TrimSpace(a.Goal) == "" {
		return nil, fmt.Errorf("suggest_autonomous: goal is required")
	}
	brief := conversation.AutonomyBrief{
		Goal:         strings.TrimSpace(a.Goal),
		DoneWhen:     compactStrings(a.DoneWhen),
		Constraints:  compactStrings(a.Constraints),
		ReviewPoints: compactStrings(a.ReviewPoints),
	}
	if call.Svc.Conversations != nil && strings.TrimSpace(call.ConversationID) != "" {
		briefJSON, err := json.Marshal(brief)
		if err != nil {
			return nil, fmt.Errorf("suggest_autonomous: marshal brief: %w", err)
		}
		reason := strings.TrimSpace(a.Reason)
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
			return nil, fmt.Errorf("suggest_autonomous: marshal brief revisions: %w", err)
		}
		sourceKind := "direct_user_request"
		if strings.TrimSpace(a.SourcePlanPath) != "" || strings.TrimSpace(a.SourceSpecPath) != "" {
			sourceKind = "accepted_plan"
		}
		if err := call.Svc.Conversations.SaveAutonomyRun(ctx, conversation.AutonomyRun{
			ConversationID: call.ConversationID,
			State:          "running",
			SourceKind:     sourceKind,
			SourcePlanPath: strings.TrimSpace(a.SourcePlanPath),
			SourceSpecPath: strings.TrimSpace(a.SourceSpecPath),
			BriefJSON:      string(briefJSON),
			RevisionsJSON:  string(revsJSON),
			DecisionsJSON:  "[]",
			ReviewJSON:     "{}",
		}); err != nil {
			return nil, fmt.Errorf("suggest_autonomous: save autonomy ledger: %w", err)
		}
	}
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("suggest_autonomous: autonomous mode is not available (no profile broker wired)")
	}
	if err := call.Svc.EnterProfile(call.ConversationID, "autonomous"); err != nil {
		return nil, fmt.Errorf("suggest_autonomous: entering autonomous mode: %w", err)
	}
	msg := "Entered autonomous mode. Work to the approved run brief, capture meaningful in-scope decisions, continue unless a high-risk boundary is crossed, and request autonomous exit when the brief is satisfied."
	if r := strings.TrimSpace(a.Reason); r != "" {
		msg = "Entered autonomous mode: " + r + ".\n\n" + msg
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: msg}, nil
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
	if err := updateAutonomyRunState(ctx, call, "abandoned"); err != nil {
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

// RequestAutonomousExit asks the user to begin final decision review and leave
// autonomous mode after successful completion.
func RequestAutonomousExit() capabilities.Capability { return requestAutonomousExitCap{} }

func (requestAutonomousExitCap) Name() string                   { return "request_autonomous_exit" }
func (requestAutonomousExitCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (requestAutonomousExitCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (requestAutonomousExitCap) Description() string {
	return "Request final review and exit from autonomous mode after the approved run brief is satisfied. The user is shown a y/n/d/c prompt; on approval this version exits autonomous mode after reporting that decision review should be performed."
}
func (requestAutonomousExitCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"summary":{"type":"string","description":"Concise summary of completed autonomous work."},
			"verification":{"type":"string","description":"Checks or verification completed before requesting exit."}
		}
	}`)
}
func (requestAutonomousExitCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a struct {
		Summary      string `json:"summary"`
		Verification string `json:"verification"`
	}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("request_autonomous_exit: parse args: %w", err)
		}
	}
	if err := updateAutonomyRunState(ctx, call, "completed"); err != nil {
		return nil, fmt.Errorf("request_autonomous_exit: update autonomy ledger: %w", err)
	}
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("request_autonomous_exit: autonomous mode is not available (no profile broker wired)")
	}
	if err := call.Svc.EnterProfile(call.ConversationID, "default"); err != nil {
		return nil, fmt.Errorf("request_autonomous_exit: leaving autonomous mode: %w", err)
	}
	parts := []string{"Autonomous run review approved; exited autonomous mode."}
	if s := strings.TrimSpace(a.Summary); s != "" {
		parts = append(parts, "Summary: "+s)
	}
	if v := strings.TrimSpace(a.Verification); v != "" {
		parts = append(parts, "Verification: "+v)
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: strings.Join(parts, "\n")}, nil
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

func updateAutonomyRunState(ctx context.Context, call *capabilities.Call, state string) error {
	if call.Svc.Conversations == nil || strings.TrimSpace(call.ConversationID) == "" {
		return nil
	}
	run, err := call.Svc.Conversations.GetAutonomyRun(ctx, call.ConversationID)
	if err != nil {
		// Exiting autonomous mode should remain possible even if the run predates
		// the ledger or the ledger was unavailable in this execution environment.
		return nil
	}
	run.State = state
	run.UpdatedAt = time.Now()
	return call.Svc.Conversations.SaveAutonomyRun(ctx, run)
}
