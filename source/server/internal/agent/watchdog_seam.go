package agent

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/llm"
)

// WatchdogDecision is the protocol gate's verdict on a proposed W/X tool call.
// Action is one of: "allow", "challenge", "block", "escalate" ("" == allow).
type WatchdogDecision struct {
	Action    string
	Protocol  string
	Challenge string
}

// WatchdogGate, when set on ToolLoopInput, is called before each W/X tool
// executes. It supervises protocol compliance independent of the permission
// mode. nil = disabled (the loop behaves exactly as before).
type WatchdogGate func(ctx context.Context, toolName string, args json.RawMessage, transcript []llm.Message) WatchdogDecision
