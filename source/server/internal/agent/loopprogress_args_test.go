package agent

import (
	"testing"

	"cercano/source/server/internal/agenttools"
)

// TestLoopProgressEvent_CarriesArgsJSON guards the hop where sub-agent tool
// args were previously dropped: a ProgressEvent's ArgsJSON must survive into
// the parent LoopEvent so the runner can summarize it for the child tab.
func TestLoopProgressEvent_CarriesArgsJSON(t *testing.T) {
	progress := agenttools.ProgressEvent{
		SubAgentID: "sub1",
		ArgsJSON:   `{"command":"ls -la"}`,
	}
	le := loopProgressEvent("tid", "Bash", progress)
	if le.ArgsJSON != progress.ArgsJSON {
		t.Fatalf("LoopEvent.ArgsJSON = %q, want %q", le.ArgsJSON, progress.ArgsJSON)
	}
}
