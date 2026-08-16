package builtins

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
)

type captureDecisionCap struct{}

type captureDecisionArgs struct {
	DecisionPoint    string                                         `json:"decision_point"`
	Options          []conversation.AutonomyDecisionOption          `json:"options"`
	Counterarguments []conversation.AutonomyDecisionCounterargument `json:"counterarguments"`
	Recommendation   string                                         `json:"recommendation"`
	ChosenPath       string                                         `json:"chosen_path"`
	WhyCleanest      string                                         `json:"why_cleanest"`
	Reversibility    string                                         `json:"reversibility"`
	StopRequired     bool                                           `json:"stop_required"`
	StopReason       string                                         `json:"stop_reason"`
}

// CaptureDecision records a meaningful autonomous fork after the agent has used
// the design-decision protocol. It is TierW because it mutates Cercano's own
// ledger, but in the default permissive mode it remains frictionless; strict
// users can still gate the write.
func CaptureDecision() capabilities.Capability { return captureDecisionCap{} }

func (captureDecisionCap) Name() string                   { return "capture_decision" }
func (captureDecisionCap) Tier() capabilities.Tier        { return capabilities.TierW }
func (captureDecisionCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (captureDecisionCap) Description() string {
	return "Record a meaningful autonomous decision fork in the run ledger. Before calling this, use the design-decision protocol: enumerate real options, compare cost/risk/reward/side effects, flag hacks, argue counterarguments, pick the cleanest in-scope path, and continue unless the choice is effectively irreversible or crosses scope/security/destructive boundaries."
}
func (captureDecisionCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"decision_point":{"type":"string","description":"The meaningful fork being decided."},
			"options":{"type":"array","items":{"type":"object","properties":{
				"title":{"type":"string"},
				"cost":{"type":"string"},
				"risk":{"type":"string"},
				"reward":{"type":"string"},
				"side_effects":{"type":"string"},
				"hack_flags":{"type":"array","items":{"type":"string"}}
			},"required":["title","cost","risk","reward","side_effects"]}},
			"counterarguments":{"type":"array","items":{"type":"object","properties":{
				"option":{"type":"string"},
				"strongest_case":{"type":"string"}
			},"required":["option","strongest_case"]}},
			"recommendation":{"type":"string","description":"The option recommended by the decision protocol."},
			"chosen_path":{"type":"string","description":"The path the agent will take."},
			"why_cleanest":{"type":"string","description":"Why this is the cleanest acceptable choice, not just the fastest hack."},
			"reversibility":{"type":"string","enum":["easy","moderate","hard","effectively_irreversible"]},
			"stop_required":{"type":"boolean","description":"Whether this fork crosses the high-risk threshold and requires user input before continuing."},
			"stop_reason":{"type":"string","description":"Required when stop_required is true."}
		},
		"required":["decision_point","options","counterarguments","recommendation","chosen_path","why_cleanest","reversibility","stop_required"]
	}`)
}
func (captureDecisionCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a captureDecisionArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("capture_decision: parse args: %w", err)
		}
	}
	if err := validateDecisionArgs(a); err != nil {
		return nil, fmt.Errorf("capture_decision: %w", err)
	}
	if call.Svc.Conversations == nil {
		return nil, fmt.Errorf("capture_decision: autonomy ledger is not available")
	}
	if strings.TrimSpace(call.ConversationID) == "" {
		return nil, fmt.Errorf("capture_decision: conversation id is required")
	}
	run, err := call.Svc.Conversations.GetAutonomyRun(ctx, call.ConversationID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("capture_decision: no autonomous run ledger for conversation %s", call.ConversationID)
		}
		return nil, fmt.Errorf("capture_decision: load autonomy ledger: %w", err)
	}
	var decisions []conversation.AutonomyDecision
	if strings.TrimSpace(run.DecisionsJSON) != "" {
		if err := json.Unmarshal([]byte(run.DecisionsJSON), &decisions); err != nil {
			return nil, fmt.Errorf("capture_decision: decode existing decisions: %w", err)
		}
	}
	entry := conversation.AutonomyDecision{
		Sequence:         len(decisions) + 1,
		Timestamp:        time.Now(),
		DecisionPoint:    strings.TrimSpace(a.DecisionPoint),
		Options:          normalizeDecisionOptions(a.Options),
		Counterarguments: normalizeCounterarguments(a.Counterarguments),
		Recommendation:   strings.TrimSpace(a.Recommendation),
		ChosenPath:       strings.TrimSpace(a.ChosenPath),
		WhyCleanest:      strings.TrimSpace(a.WhyCleanest),
		Reversibility:    strings.TrimSpace(a.Reversibility),
		StopRequired:     a.StopRequired,
		StopReason:       strings.TrimSpace(a.StopReason),
	}
	decisions = append(decisions, entry)
	decisionsJSON, err := json.Marshal(decisions)
	if err != nil {
		return nil, fmt.Errorf("capture_decision: encode decisions: %w", err)
	}
	run.DecisionsJSON = string(decisionsJSON)
	run.UpdatedAt = time.Now()
	if err := call.Svc.Conversations.SaveAutonomyRun(ctx, run); err != nil {
		return nil, fmt.Errorf("capture_decision: save autonomy ledger: %w", err)
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: fmt.Sprintf("Captured autonomous decision #%d: %s", entry.Sequence, entry.DecisionPoint)}, nil
}

func validateDecisionArgs(a captureDecisionArgs) error {
	if strings.TrimSpace(a.DecisionPoint) == "" {
		return fmt.Errorf("decision_point is required")
	}
	if len(a.Options) < 2 {
		return fmt.Errorf("at least two real options are required")
	}
	for i, opt := range a.Options {
		if strings.TrimSpace(opt.Title) == "" || strings.TrimSpace(opt.Cost) == "" || strings.TrimSpace(opt.Risk) == "" || strings.TrimSpace(opt.Reward) == "" || strings.TrimSpace(opt.SideEffects) == "" {
			return fmt.Errorf("options[%d] must include title, cost, risk, reward, and side_effects", i)
		}
	}
	if len(a.Counterarguments) == 0 {
		return fmt.Errorf("at least one counterargument is required")
	}
	for i, c := range a.Counterarguments {
		if strings.TrimSpace(c.Option) == "" || strings.TrimSpace(c.StrongestCase) == "" {
			return fmt.Errorf("counterarguments[%d] must include option and strongest_case", i)
		}
	}
	if strings.TrimSpace(a.Recommendation) == "" {
		return fmt.Errorf("recommendation is required")
	}
	if strings.TrimSpace(a.ChosenPath) == "" {
		return fmt.Errorf("chosen_path is required")
	}
	if strings.TrimSpace(a.WhyCleanest) == "" {
		return fmt.Errorf("why_cleanest is required")
	}
	switch strings.TrimSpace(a.Reversibility) {
	case "easy", "moderate", "hard", "effectively_irreversible":
	default:
		return fmt.Errorf("reversibility must be easy, moderate, hard, or effectively_irreversible")
	}
	if a.StopRequired && strings.TrimSpace(a.StopReason) == "" {
		return fmt.Errorf("stop_reason is required when stop_required is true")
	}
	return nil
}

func normalizeDecisionOptions(in []conversation.AutonomyDecisionOption) []conversation.AutonomyDecisionOption {
	out := make([]conversation.AutonomyDecisionOption, 0, len(in))
	for _, opt := range in {
		out = append(out, conversation.AutonomyDecisionOption{
			Title:       strings.TrimSpace(opt.Title),
			Cost:        strings.TrimSpace(opt.Cost),
			Risk:        strings.TrimSpace(opt.Risk),
			Reward:      strings.TrimSpace(opt.Reward),
			SideEffects: strings.TrimSpace(opt.SideEffects),
			HackFlags:   compactStrings(opt.HackFlags),
		})
	}
	return out
}

func normalizeCounterarguments(in []conversation.AutonomyDecisionCounterargument) []conversation.AutonomyDecisionCounterargument {
	out := make([]conversation.AutonomyDecisionCounterargument, 0, len(in))
	for _, c := range in {
		out = append(out, conversation.AutonomyDecisionCounterargument{
			Option:        strings.TrimSpace(c.Option),
			StrongestCase: strings.TrimSpace(c.StrongestCase),
		})
	}
	return out
}
