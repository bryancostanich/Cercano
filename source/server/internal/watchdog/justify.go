package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"cercano/source/server/internal/agenttools"
)

// justifyTool implements agenttools.Tool and lets the main model override a
// watchdog challenge by recording a justification reason.
type justifyTool struct {
	w              *Watchdog
	conversationID string
}

// JustifyTool returns an agenttools.Tool that, when executed, records a
// justification for the most recently challenged action in conversationID.
func (w *Watchdog) JustifyTool(conversationID string) agenttools.Tool {
	return &justifyTool{w: w, conversationID: conversationID}
}

func (j *justifyTool) Name() string { return "justify" }

func (j *justifyTool) Description() string {
	return "Override a watchdog protocol challenge by recording a justification reason."
}

func (j *justifyTool) Permission() agenttools.Permission { return agenttools.PermR }

func (j *justifyTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["reason"],
		"properties": {
			"reason": {"type": "string"}
		}
	}`)
}

type justifyArgs struct {
	Reason string `json:"reason"`
}

func (j *justifyTool) Execute(_ context.Context, args json.RawMessage) (*agenttools.Result, error) {
	var a justifyArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("justify: parse args: %w", err)
	}
	key := j.w.lastChallengedKey(j.conversationID)
	if key == "" {
		return &agenttools.Result{
			Type: agenttools.ResultText,
			Text: "no active watchdog challenge to justify",
		}, nil
	}
	j.w.recordJustify(j.conversationID, key)
	j.w.recordAudit(context.Background(), AuditEvent{
		ConversationID: j.conversationID,
		EventType:      "resolution",
		Decision:       "allow",
		Key:            key,
		Resolution:     "justify",
		Reason:         a.Reason,
	})
	log.Printf("watchdog: justify override recorded for conversation %q key %q reason: %s", j.conversationID, key, a.Reason)
	j.w.emitEcho("main", "justify: "+a.Reason)
	return &agenttools.Result{
		Type: agenttools.ResultText,
		Text: "watchdog override recorded: " + a.Reason,
	}, nil
}
