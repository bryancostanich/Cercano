package slash

import (
	"strings"

	"cercano/source/server/pkg/agentclient"
)

// RegisterAuto wires /auto, the lightweight entrypoint to autonomous mode. The
// command does not flip the profile directly: it submits a normal user turn so
// the model can draft the run brief and call suggest_autonomous for the standard
// y/n/d/c approval gate.
func RegisterAuto(r *Registry, _ *agentclient.Client) {
	r.Register(Command{
		Name: "auto",
		Help: "Start an autonomous run: /auto <goal>. The assistant drafts a brief for approval before autonomous mode starts.",
		Handler: func(args []string) Result {
			goal := strings.TrimSpace(strings.Join(args, " "))
			if goal == "" {
				return Result{Kind: ResultSubmitPrompt, Text: "I want to start an autonomous run. Help me define a concise autonomous run brief with goal, done_when, constraints, and review_points, then call suggest_autonomous for approval. Do not enter autonomous mode until I approve the brief."}
			}
			return Result{Kind: ResultSubmitPrompt, Text: "Start autonomous mode for this goal: " + goal + "\n\nDraft a concise autonomous run brief with goal, done_when, constraints, and review_points, then call suggest_autonomous for user approval. Do not enter autonomous mode until the brief is approved."}
		},
	})
}
