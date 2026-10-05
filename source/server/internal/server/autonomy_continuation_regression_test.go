package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// TestStreamToolLoop_AutonomousContinuation_CompletionOmitsUnwantedGenericMessage verifies
// that when a chained run completes normally (not blocked, not paused), the server does NOT
// emit a generic "autonomous continuation ended — waiting for human input" message.
func TestStreamToolLoop_AutonomousContinuation_CompletionOmitsUnwantedGenericMessage(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-completion", "running")
	// Turn 2 completes the run (completed) so the chain stops after continuation
	completer := &ledgerCompleter{store: store, runID: run.RunID, conv: "conv-completion", state: "completed"}
	reg := agenttools.NewRegistry()
	reg.MustRegister(completer)
	srv.SetToolRegistry(reg)

	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "step one done."}},
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "complete_run",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "step two done."}},
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-completion"}, stream)
	if err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (turn 1 + continuation's tool round trip)", prov.calls)
	}
	frs := finalResponses(stream.sent)
	if len(frs) != 2 {
		t.Fatalf("FinalResponse count = %d, want 2", len(frs))
	}
	if frs[0].GetOutput() != "step one done." || frs[1].GetOutput() != "step two done." {
		t.Errorf("final outputs = %q, %q", frs[0].GetOutput(), frs[1].GetOutput())
	}


	notes := progressNotes(stream.sent)
	for _, n := range notes {
		if strings.Contains(n, "autonomous continuation ended") && strings.Contains(n, "waiting for human input") {
			t.Errorf("found unwanted generic 'autonomous continuation ended — waiting for human input' message in progress notes: %v", notes)
		}
	}

	// Verify the run completed normally (not blocked, not paused)
	// When a run is completed, it's no longer active, so we need to check the latest run
	got, err := store.GetLatestAutonomyRun(context.Background(), "conv-completion")
	if err != nil {
		t.Fatalf("GetLatestAutonomyRun: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("run state = %q, want completed (normal completion, not blocked/paused)", got.State)
	}
}

// TestStreamToolLoop_AutonomousContinuation_AbandonedOmitsUnwantedGenericMessage verifies
// that when a chained run is abandoned (not running anymore), the server does NOT emit
// a generic "autonomous continuation ended — waiting for human input" message.
func TestStreamToolLoop_AutonomousContinuation_AbandonedOmitsUnwantedGenericMessage(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-abandoned", "running")
	// Turn 2 abandons the run (abandoned) so the chain stops after continuation
	completer := &ledgerCompleter{store: store, runID: run.RunID, conv: "conv-abandoned", state: "abandoned"}
	reg := agenttools.NewRegistry()
	reg.MustRegister(completer)
	srv.SetToolRegistry(reg)

	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "step one done."}},
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "complete_run",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "step two done."}},
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-abandoned"}, stream)
	if err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (turn 1 + continuation's tool round trip)", prov.calls)
	}
	frs := finalResponses(stream.sent)
	if len(frs) != 2 {
		t.Fatalf("FinalResponse count = %d, want 2", len(frs))
	}


	notes := progressNotes(stream.sent)
	for _, n := range notes {
		if strings.Contains(n, "autonomous continuation ended") && strings.Contains(n, "waiting for human input") {
			t.Errorf("found unwanted generic 'autonomous continuation ended — waiting for human input' message in progress notes: %v", notes)
		}
	}

	// Verify the run was abandoned (not blocked, not paused)
	// When a run is abandoned, it's no longer active, so we need to check the latest run
	got, err := store.GetLatestAutonomyRun(context.Background(), "conv-abandoned")
	if err != nil {
		t.Fatalf("GetLatestAutonomyRun: %v", err)
	}
	if got.State != "abandoned" {
		t.Errorf("run state = %q, want abandoned (normal abandonment, not blocked/paused)", got.State)
	}
}