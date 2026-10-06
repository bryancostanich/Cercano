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

type reportAutonomousBlockerCap struct{}

// ReportAutonomousBlocker is the model's explicit "I am blocked and need the
// user" capability for an autonomous run. It records a structured blocker
// (reason required) on the durable ledger row and leaves the run in state
// "running": the host's continuation gate stops chaining turns while a blocker
// is recorded, and the user's next explicit message clears it and resumes the
// run. It is deliberately R-tier — reporting that you are blocked must never
// itself need approval — and it never touches approval states: a
// review_pending run is already paused for a human and errors instead.
func ReportAutonomousBlocker() capabilities.Capability { return reportAutonomousBlockerCap{} }

func (reportAutonomousBlockerCap) Name() string                   { return "report_autonomous_blocker" }
func (reportAutonomousBlockerCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (reportAutonomousBlockerCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (reportAutonomousBlockerCap) Description() string {
	return "Explicitly pause the active autonomous run because you are blocked and need the user: a required approval, decision, credential, missing resource, or external input. Give a concrete, specific reason describing what you need. The run stays active (state stays running); the host stops chaining autonomous turns immediately and resumes only on the user's next message."
}
func (reportAutonomousBlockerCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"reason":{"type":"string","description":"What is blocking the run and what you need from the user. Be specific."}
		},
		"required":["reason"],
		"additionalProperties":false}`)
}

func (reportAutonomousBlockerCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("report_autonomous_blocker: parse args: %w", err)
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return nil, fmt.Errorf("report_autonomous_blocker: reason is required — describe what you need from the user")
	}
	// "running" only: a review_pending run is an in-flight approval and must be
	// preserved untouched, so a blocker report there errors instead of editing
	// the ledger.
	store, run, err := requireActiveAutonomyRun(ctx, call, "report_autonomous_blocker", "running")
	if err != nil {
		return nil, err
	}
	blocker := conversation.AutonomyBlocker{Reason: reason, RecordedAt: time.Now().UTC()}
	data, err := json.Marshal(blocker)
	if err != nil {
		return nil, fmt.Errorf("report_autonomous_blocker: encode blocker: %w", err)
	}
	run.BlockerJSON = string(data)
	run.UpdatedAt = time.Now()
	if err := store.UpdateAutonomyRun(ctx, run); err != nil {
		return nil, fmt.Errorf("report_autonomous_blocker: record blocker: %w", err)
	}
	return &capabilities.Result{
		Type: capabilities.ResultText,
		Text: "Autonomous run paused: blocker recorded (the run stays active). The host will not run further turns on its own; the user's next message resumes the run.",
	}, nil
}
