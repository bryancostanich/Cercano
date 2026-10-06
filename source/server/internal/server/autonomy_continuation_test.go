package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"
)

// createAutonomyRun seeds one durable autonomy ledger row in the given state
// for conv, mirroring what request_autonomous_execution's approval path
// writes. Returns the stored run (with its generated run id).
func createAutonomyRun(t *testing.T, store conversation.Store, conv, state string) conversation.AutonomyRun {
	t.Helper()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, conv, "", "test-model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	run, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{
		ConversationID: conv,
		State:          state,
		BriefJSON:      `{"goal":"ship the feature","done_when":"tests pass"}`,
	})
	if err != nil {
		t.Fatalf("CreateAutonomyRun: %v", err)
	}
	return run
}

// finalResponses extracts every FinalResponse payload from a fakeStream's sent
// slice, in order. A host-continued request sends one per model turn.
func finalResponses(sent []*proto.StreamProcessResponse) []*proto.ProcessRequestResponse {
	var out []*proto.ProcessRequestResponse
	for _, m := range sent {
		if fr := m.GetFinalResponse(); fr != nil {
			out = append(out, fr)
		}
	}
	return out
}

// progressNotes extracts every ProgressUpdate message from a fakeStream's sent
// slice, in order. The host announces/pauses autonomous continuation with these
// structured events instead of prose.
func progressNotes(sent []*proto.StreamProcessResponse) []string {
	var out []string
	for _, m := range sent {
		if p := m.GetProgress(); p != nil {
			out = append(out, p.GetMessage())
		}
	}
	return out
}

// TestStreamToolLoop_AutonomousContinuation_ChainsTurnsWhileRunning is the core
// repro: after a normal successful turn, while the durable ledger still holds
// the conversation's autonomy run in "running", the host chains another turn on
// the same conversation with a structured host-authored input — no user message
// and no model-prose parsing involved.
func TestStreamToolLoop_AutonomousContinuation_ChainsTurnsWhileRunning(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-cont", "running")
	// Turn 2 ends the run (review_pending) so the chain deterministically stops
	// after exactly one continuation.
	completer := &ledgerCompleter{store: store, runID: run.RunID, conv: "conv-cont", state: "review_pending"}
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
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-cont"}, stream)
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

	// A structured Progress event announced the host-driven continuation.
	notes := progressNotes(stream.sent)
	var announced bool
	for _, n := range notes {
		if strings.Contains(n, "autonomous continuation") && strings.Contains(n, run.RunID) {
			announced = true
		}
	}
	if !announced {
		t.Errorf("no continuation announcement in progress notes: %v", notes)
	}
	// Verify no unwanted generic "autonomous continuation ended — waiting for human input" message
	var foundGenericMessage bool
	for _, n := range notes {
		if strings.Contains(n, "autonomous continuation ended") && strings.Contains(n, "waiting for human input") {
			foundGenericMessage = true
		}
	}
	if foundGenericMessage {
		t.Errorf("found unwanted generic 'autonomous continuation ended — waiting for human input' message in progress notes: %v", notes)
	}
	// Turn-start ledger rehydration: the running run restored the profile
	// before the first turn even without a GetSessionProfile call.
	if got := srv.profileBroker.ActiveName("conv-cont"); got != "autonomous" {
		t.Errorf("profile after turn = %q, want autonomous (restored at turn start)", got)
	}

	// The continuation turn's provider input is the host marker, appended after
	// the first turn's history (user "begin", assistant "step one done.").
	if len(prov.seen) != 3 {
		t.Fatalf("provider seen = %d calls, want 3 (turn 1 + continuation's two tool-loop calls)", len(prov.seen))
	}
	turn2 := prov.seen[2]
	if len(turn2) != 5 {
		t.Fatalf("continuation provider input = %d messages, want 5", len(turn2))
	}
	marker := turn2[2]
	if marker.Role != llm.RoleUser || !strings.Contains(marker.Blocks[0].Text, autonomyContinuationMarkerPrefix) {
		t.Errorf("continuation input should be a user message carrying the host marker, got role=%v text=%q",
			marker.Role, marker.Blocks[0].Text)
	}
	if !strings.Contains(marker.Blocks[0].Text, run.RunID) {
		t.Errorf("continuation marker should carry run id %q: %q", run.RunID, marker.Blocks[0].Text)
	}

	// Persistence keeps the host author as source metadata: the continuation
	// turn is stored with role "system" (host-authored), never "user" — the
	// host must not impersonate a human author in the durable history.
	turns, err := store.GetTurns(context.Background(), "conv-cont")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if len(turns) < 4 {
		t.Fatalf("expected at least 4 persisted turns, got %d", len(turns))
	}
	if turns[2].Role != string(llm.RoleSystem) || !strings.Contains(turns[2].Content, autonomyContinuationMarkerPrefix) {
		t.Errorf("persisted continuation turn should be a system-role host marker, got role=%q content=%q",
			turns[2].Role, turns[2].Content)
	}
	if last := turns[len(turns)-1]; last.Role != "assistant" || last.Content != "step two done." {
		t.Errorf("last persisted turn should be the continuation final text, got %+v", last)
	}
	// The run moved to review_pending during the continuation turn and the
	// chain stopped — never restarted.
	got, err := store.GetActiveAutonomyRun(context.Background(), "conv-cont")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.State != "review_pending" {
		t.Errorf("run state after continuation = %q, want review_pending", got.State)
	}
}

// TestStreamToolLoop_AutonomousContinuation_StopsWhenNotRunning verifies the
// blocking gate: a run in review_pending waits for human review, so a normal
// successful turn does NOT chain another one.
func TestStreamToolLoop_AutonomousContinuation_StopsWhenNotRunning(t *testing.T) {
	srv, store := newServerWithStore(t)
	createAutonomyRun(t, store, "conv-review", "review_pending")
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "requested review."}}},
		caps:    inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-review"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (review_pending blocks continuation)", prov.calls)
	}
	if frs := finalResponses(stream.sent); len(frs) != 1 {
		t.Fatalf("FinalResponse count = %d, want 1", len(frs))
	}
}

// TestStreamToolLoop_AutonomousContinuation_StopsWhenRunCompletesMidTurn
// verifies the gate reacts to ledger transitions that happen DURING the turn:
// a tool that completes the run ends the chain even though the turn itself
// completes normally.
func TestStreamToolLoop_AutonomousContinuation_StopsWhenRunCompletesMidTurn(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-done", "running")
	completer := &ledgerCompleter{store: store, runID: run.RunID, conv: "conv-done", state: "completed"}
	reg := agenttools.NewRegistry()
	reg.MustRegister(completer)
	srv.SetToolRegistry(reg)

	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "complete_run",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "finished the run."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}},
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-done"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	// Turn 1 consumed 2 scripts (tool_use + final text); the third script would
	// only be consumed if a continuation turn ran — it must not.
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (both in turn 1; completion blocks continuation)", prov.calls)
	}
	if frs := finalResponses(stream.sent); len(frs) != 1 {
		t.Fatalf("FinalResponse count = %d, want 1", len(frs))
	}
	turns, err := store.GetTurns(context.Background(), "conv-done")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	for _, tn := range turns {
		if strings.Contains(tn.Content, "SHOULD NOT RUN") || strings.Contains(tn.Content, autonomyContinuationMarkerPrefix) {
			t.Errorf("continuation ran despite completed run: %+v", tn)
		}
	}
}

// TestStreamToolLoop_AutonomousContinuation_NoProgressBounded verifies the
// safeguard: consecutive turns that produce no durable ledger progress and no
// real tool work are bounded. After autonomyNoProgressLimit consecutive
// no-progress turns the host pauses the still-active run and reports it as a
// structured progress event — never as prose in the final response.
func TestStreamToolLoop_AutonomousContinuation_NoProgressBounded(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-stuck", "running")
	scripts := make([][]llm.Block, 0, 10)
	for i := 0; i < 10; i++ {
		scripts = append(scripts, []llm.Block{{Type: llm.BlockText, Text: "nothing new."}})
	}
	prov := &scriptedProvider{scripts: scripts, caps: inferenceCapabilities()}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-stuck"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	want := autonomyNoProgressLimit // initial turn + (limit-1) continuations, then the bound pauses
	if prov.calls != want {
		t.Fatalf("provider calls = %d, want %d (no-progress bound: %d consecutive ledger-silent turns)", prov.calls, want, autonomyNoProgressLimit)
	}
	if frs := finalResponses(stream.sent); len(frs) != want {
		t.Fatalf("FinalResponse count = %d, want %d", len(frs), want)
	}

	// The pause is a structured progress notice mentioning the still-active run.
	var paused bool
	for _, n := range progressNotes(stream.sent) {
		if strings.Contains(n, "paused") && strings.Contains(n, run.RunID) {
			paused = true
		}
	}
	if !paused {
		t.Errorf("no structured pause notice for run %s in progress notes", run.RunID)
	}
	// The run must remain running — the safeguard pauses, it never mutates the
	// ledger.
	got, err := store.GetActiveAutonomyRun(context.Background(), "conv-stuck")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.State != "running" {
		t.Errorf("safeguard mutated run state to %q, want running", got.State)
	}
}

// TestStreamToolLoop_AutonomousContinuation_ProductiveWorkThenCompletion
// verifies that real tool work resets the no-progress bound, so a run that
// keeps doing successful non-bookkeeping work is never paused. The chain runs
// deterministically through more than autonomyNoProgressLimit productive turns
// (one successful tool execution each) and then stops exactly when a turn
// completes the run.
func TestStreamToolLoop_AutonomousContinuation_ProductiveWorkThenCompletion(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-work", "running")
	completer := &ledgerCompleter{store: store, runID: run.RunID, conv: "conv-work", state: "completed"}
	reg := agenttools.NewRegistry()
	reg.MustRegister(completer)
	reg.MustRegister(&workingTool{})
	srv.SetToolRegistry(reg)

	const productiveTurns = 4 // > autonomyNoProgressLimit
	scripts := make([][]llm.Block, 0, 2*(productiveTurns+1))
	for i := 0; i < productiveTurns; i++ {
		scripts = append(scripts,
			[]llm.Block{{Type: llm.BlockToolUse, ToolUseID: fmt.Sprintf("w%d", i), ToolName: "do_work",
				ToolInput: json.RawMessage(`{}`)}},
			[]llm.Block{{Type: llm.BlockText, Text: "turn done."}},
		)
	}
	// Final turn completes the run mid-turn and still answers with text.
	scripts = append(scripts,
		[]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "c1", ToolName: "complete_run",
			ToolInput: json.RawMessage(`{}`)}},
		[]llm.Block{{Type: llm.BlockText, Text: "all done."}},
	)
	prov := &scriptedProvider{scripts: scripts, caps: inferenceCapabilities()}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-work"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != len(scripts) {
		t.Fatalf("provider calls = %d, want %d (%d turns × tool round trip + final text)",
			prov.calls, len(scripts), productiveTurns+1)
	}
	if frs := finalResponses(stream.sent); len(frs) != productiveTurns+1 {
		t.Fatalf("FinalResponse count = %d, want %d", len(frs), productiveTurns+1)
	}
	for _, n := range progressNotes(stream.sent) {
		if strings.Contains(n, "paused") {
			t.Errorf("productive run was paused by the no-progress safeguard: %q", n)
		}
	}
	// Each chained turn surfaced that its real tool work reset the bound (the
	// ledger stayed quiet — the tools write files, not ledger rows).
	var workNoticeSeen bool
	for _, n := range progressNotes(stream.sent) {
		if strings.Contains(n, autonomyWorkNotice()) {
			workNoticeSeen = true
		}
	}
	if !workNoticeSeen {
		t.Errorf("no work-reset notice (%q) in progress notes", autonomyWorkNotice())
	}

	// Every persisted host-continuation marker is stored as source metadata
	// (system role) yet reaches the provider as a user-role message — otherwise
	// providers that reject mid-history system messages would silently drop the
	// host's input. Turn 3's first attempt carries the first persisted marker
	// in assembled history at index 4.
	turn3 := prov.seen[4]
	if len(turn3) != 9 {
		t.Fatalf("turn 3 first attempt = %d messages, want 9", len(turn3))
	}
	persistedMarker := turn3[4]
	if persistedMarker.Role != llm.RoleUser || !strings.Contains(persistedMarker.Blocks[0].Text, autonomyContinuationMarkerPrefix) {
		t.Errorf("persisted system-role marker must map to a provider user-role message, got role=%v text=%q",
			persistedMarker.Role, persistedMarker.Blocks[0].Text)
	}
	if !strings.Contains(persistedMarker.Blocks[0].Text, run.RunID) {
		t.Errorf("persisted marker should still carry run id %q: %q", run.RunID, persistedMarker.Blocks[0].Text)
	}
	turns, err := store.GetTurns(context.Background(), "conv-work")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	var markerCount int
	for _, tn := range turns {
		if strings.Contains(tn.Content, autonomyContinuationMarkerPrefix) {
			markerCount++
			if tn.Role != string(llm.RoleSystem) {
				t.Errorf("persisted marker turn role = %q, want %q (host-authored source metadata)",
					tn.Role, llm.RoleSystem)
			}
		}
	}
	if markerCount != productiveTurns {
		t.Errorf("persisted marker turns = %d, want %d", markerCount, productiveTurns)
	}
}

// TestStreamToolLoop_AutonomousContinuation_FailedToolsDoNotReset verifies the
// other half of the work-evidence rule: a tool that executes but FAILS is not
// work. A run whose every turn calls a failing tool still hits the no-progress
// bound and pauses after autonomyNoProgressLimit turns.
func TestStreamToolLoop_AutonomousContinuation_FailedToolsDoNotReset(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-fail", "running")
	reg := agenttools.NewRegistry()
	reg.MustRegister(&failingTool{})
	srv.SetToolRegistry(reg)

	scripts := make([][]llm.Block, 0, 2*autonomyNoProgressLimit+2)
	for i := 0; i < autonomyNoProgressLimit; i++ {
		scripts = append(scripts,
			[]llm.Block{{Type: llm.BlockToolUse, ToolUseID: fmt.Sprintf("f%d", i), ToolName: "failing_work",
				ToolInput: json.RawMessage(`{}`)}},
			[]llm.Block{{Type: llm.BlockText, Text: "tried and failed."}},
		)
	}
	// Enough scripts that an unbounded chain would keep going — it must not.
	scripts = append(scripts, [][]llm.Block{
		{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}},
		{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}},
	}...)
	prov := &scriptedProvider{scripts: scripts, caps: inferenceCapabilities()}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-fail"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != 2*autonomyNoProgressLimit {
		t.Fatalf("provider calls = %d, want %d (%d turns × tool round trip; failed tools never reset the bound)",
			prov.calls, 2*autonomyNoProgressLimit, autonomyNoProgressLimit)
	}
	if frs := finalResponses(stream.sent); len(frs) != autonomyNoProgressLimit {
		t.Fatalf("FinalResponse count = %d, want %d", len(frs), autonomyNoProgressLimit)
	}
	var paused bool
	for _, n := range progressNotes(stream.sent) {
		if strings.Contains(n, "paused") && strings.Contains(n, run.RunID) {
			paused = true
		}
	}
	if !paused {
		t.Errorf("no structured pause notice after %d failed-tool turns", autonomyNoProgressLimit)
	}
	got, err := store.GetActiveAutonomyRun(context.Background(), "conv-fail")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.State != "running" {
		t.Errorf("safeguard mutated run state to %q, want running", got.State)
	}
}

// TestStreamToolLoop_AutonomousContinuation_CaptureDecisionDoesNotReset pins
// the anti-gaming rule: capture_decision is autonomy bookkeeping, and captured
// decisions (DecisionsJSON changes) are deliberately NOT ledger progress. A
// run that only records decisions — even successfully changing the durable
// ledger row every turn — still hits the no-progress bound and pauses.
func TestStreamToolLoop_AutonomousContinuation_CaptureDecisionDoesNotReset(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-cd", "running")
	recorder := &ledgerToucher{store: store, conv: "conv-cd", name: "capture_decision"}
	reg := agenttools.NewRegistry()
	reg.MustRegister(recorder)
	srv.SetToolRegistry(reg)

	scripts := make([][]llm.Block, 0, 2*autonomyNoProgressLimit+2)
	for i := 0; i < autonomyNoProgressLimit; i++ {
		scripts = append(scripts,
			[]llm.Block{{Type: llm.BlockToolUse, ToolUseID: fmt.Sprintf("d%d", i), ToolName: "capture_decision",
				ToolInput: json.RawMessage(`{}`)}},
			[]llm.Block{{Type: llm.BlockText, Text: "logged a decision."}},
		)
	}
	scripts = append(scripts, [][]llm.Block{
		{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}},
		{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}},
	}...)
	prov := &scriptedProvider{scripts: scripts, caps: inferenceCapabilities()}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-cd"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != 2*autonomyNoProgressLimit {
		t.Fatalf("provider calls = %d, want %d (capture_decision bookkeeping never resets the bound)",
			prov.calls, 2*autonomyNoProgressLimit)
	}
	if frs := finalResponses(stream.sent); len(frs) != autonomyNoProgressLimit {
		t.Fatalf("FinalResponse count = %d, want %d", len(frs), autonomyNoProgressLimit)
	}
	var paused bool
	for _, n := range progressNotes(stream.sent) {
		if strings.Contains(n, "paused") && strings.Contains(n, run.RunID) {
			paused = true
		}
	}
	if !paused {
		t.Errorf("no structured pause notice: decisions bookkeeping must not keep the chain alive indefinitely")
	}
	// The pause must not mutate the ledger row.
	got, err := store.GetActiveAutonomyRun(context.Background(), "conv-cd")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.State != "running" {
		t.Errorf("safeguard mutated run state to %q, want running", got.State)
	}
}

// TestStreamToolLoop_AutonomousContinuation_ErrorsStopChain verifies a failed
// continuation turn ends the chain instead of retrying: turn 1 succeeds, the
// gate chains turn 2, and turn 2's own failure propagates as a stream error
// (never a silent loop).
func TestStreamToolLoop_AutonomousContinuation_ErrorsStopChain(t *testing.T) {
	srv, store := newServerWithStore(t)
	createAutonomyRun(t, store, "conv-err", "running")
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "fine."}}, // turn 1: ok
			// turn 2 (continuation): no script left → RunTurn errors
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-err"}, stream)
	if err == nil {
		t.Fatal("continuation turn failure must propagate as a stream error, got nil")
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (turn 2 exhausted scripts and errored)", prov.calls)
	}
	if frs := finalResponses(stream.sent); len(frs) != 1 {
		t.Fatalf("FinalResponse count = %d, want 1 (only turn 1 completed)", len(frs))
	}
}

// TestStreamToolLoop_AutonomousProfile_RestoredAtTurnStart verifies the
// turn-start rehydration: a durable active run restores the autonomous profile
// without the client calling GetSessionProfile first.
func TestStreamToolLoop_AutonomousProfile_RestoredAtTurnStart(t *testing.T) {
	srv, store := newServerWithStore(t)
	createAutonomyRun(t, store, "conv-prof", "review_pending")
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "ok."}},  // conv-prof turn 1
			{{Type: llm.BlockText, Text: "ok2."}}, // conv-noprofile turn 1
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	if srv.profileBroker.ActiveName("conv-prof") != "default" {
		t.Fatal("precondition: profile should start default")
	}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "go", ConversationId: "conv-prof"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}
	if got := srv.profileBroker.ActiveName("conv-prof"); got != "autonomous" {
		t.Errorf("profile after turn = %q, want autonomous (restored from ledger at turn start)", got)
	}

	// No run → no profile switch.
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "go", ConversationId: "conv-noprofile"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}
	if got := srv.profileBroker.ActiveName("conv-noprofile"); got != "default" {
		t.Errorf("profile without active run = %q, want default", got)
	}
}

// TestEvaluateAutonomyContinuation_Gates unit-tests the host's algorithmic
// decision directly: structured ledger state only, never model prose.
func TestEvaluateAutonomyContinuation_Gates(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-gate", "running")

	parent := context.Background()
	turnCtx, gen, release := srv.beginTurn(parent, "conv-gate")
	defer release()

	np := 0
	prev, havePrev := srv.activeAutonomyRun(turnCtx, "conv-gate")
	if !havePrev {
		t.Fatal("precondition: active run snapshot missing")
	}

	g := srv.evaluateAutonomyContinuation(turnCtx, "conv-gate", gen, 1, prev, havePrev, newAutonomyWorkMonitor(), &np)
	if !g.cont {
		t.Fatalf("running run should continue, got gate=%+v", g)
	}
	if !strings.Contains(g.input, autonomyContinuationMarkerPrefix) || !strings.Contains(g.input, run.RunID) {
		t.Errorf("continuation input = %q", g.input)
	}

	// Superseded turn (a new user message called BeginTurn): stop.
	_, _, release2 := srv.beginTurn(parent, "conv-gate")
	defer release2()
	if g := srv.evaluateAutonomyContinuation(turnCtx, "conv-gate", gen, 1, prev, havePrev, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("superseded turn must stop continuation")
	}
	release2()

	// Canceled stream ctx: stop (no automatic relaunch; the chain only rides
	// the open client stream).
	canceled, cancel := context.WithCancel(parent)
	cancel()
	if g := srv.evaluateAutonomyContinuation(canceled, "conv-gate", gen, 1, prev, havePrev, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("canceled context must stop continuation")
	}

	// No conversation / unknown conversation: stop.
	if g := srv.evaluateAutonomyContinuation(parent, "", gen, 1, prev, havePrev, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("empty conversation id must stop continuation")
	}
	if g := srv.evaluateAutonomyContinuation(parent, "conv-unknown", gen, 1, prev, havePrev, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("unknown conversation must stop continuation")
	}

	// No-progress bound: consecutive no-progress turns stop the chain with a
	// structured pause notice and never mutate the ledger. Begin a FRESH turn:
	// the superseded generation above never becomes current again.
	np = 0
	turnCtx2, gen2, release3 := srv.beginTurn(parent, "conv-gate")
	defer release3()
	for i := 1; i <= autonomyNoProgressLimit; i++ {
		g = srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, i, run, true, newAutonomyWorkMonitor(), &np)
		if i < autonomyNoProgressLimit && !g.cont {
			t.Fatalf("turn %d should continue (below bound), got %+v", i, g)
		}
		if i == autonomyNoProgressLimit {
			if g.cont {
				t.Fatalf("turn %d should be blocked by the no-progress bound", i)
			}
			if !strings.Contains(g.notice, "paused") || !strings.Contains(g.notice, run.RunID) {
				t.Errorf("pause notice = %q", g.notice)
			}
		}
	}

	// Decisions bookkeeping is NOT progress: a turn that only appended captured
	// decisions (DecisionsJSON changed, run identity/state unchanged) must not
	// reset the bound. Seed the streak one below the limit, record a decision,
	// and verify the next gate call pauses instead of chaining.
	np = autonomyNoProgressLimit - 1
	withDecision := run
	withDecision.DecisionsJSON = run.DecisionsJSON + `{ "decision": "one" }`
	if g := srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, autonomyNoProgressLimit, withDecision, true, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("capture_decision bookkeeping must not count as ledger progress")
	}
	if np != autonomyNoProgressLimit {
		t.Errorf("decisions-only turn advanced no-progress streak to %d, want %d (it must increment, never reset)", np, autonomyNoProgressLimit)
	}
	if g := srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, autonomyNoProgressLimit+1, withDecision, true, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("run that only records decisions must be paused by the no-progress bound")
	}

	// Real work resets the bound: a monitor that observed one successful
	// non-bookkeeping tool execution chains even from a fully exhausted streak.
	work := newAutonomyWorkMonitor()
	work.Observe(runner.Event{Kind: runner.EventToolExecComplete, ToolName: "edit_file"})
	np = autonomyNoProgressLimit
	if g = srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, autonomyNoProgressLimit+2, withDecision, true, work, &np); !g.cont {
		t.Errorf("successful tool work must reset the no-progress bound, got gate=%+v", g)
	}
	if np != 0 {
		t.Errorf("work evidence must reset the streak to 0, got %d", np)
	}
	// (The work-reset ProgressUpdate itself is emitted by the turn loop —
	// see autonomyWorkNotice — not by the gate; the end-to-end productive-work
	// test below covers that emission.)

	// Work-evidence classification: failed executions and bookkeeping-only
	// executions never count as work.
	neg := newAutonomyWorkMonitor()
	neg.Observe(runner.Event{Kind: runner.EventToolExecComplete, ToolName: "edit_file", IsError: true})
	neg.Observe(runner.Event{Kind: runner.EventToolExecComplete, ToolName: "capture_decision"})
	neg.Observe(runner.Event{Kind: runner.EventSubAgent, SubAgentKind: "start"})
	if neg.HasWork() {
		t.Error("failed tools, bookkeeping tools, and sub-agent starts must not count as work")
	}

	// review_pending blocks: review waits for a human.
	if err := store.UpdateAutonomyRun(parent, conversation.AutonomyRun{
		RunID: run.RunID, ConversationID: "conv-gate", State: "review_pending"}); err != nil {
		t.Fatalf("UpdateAutonomyRun: %v", err)
	}
	np = 0
	if g := srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, 1, run, true, newAutonomyWorkMonitor(), &np); g.cont {
		t.Error("review_pending must stop continuation")
	}

	// A recorded report_autonomous_blocker stops the chain silently: the
	// model's own final response already presented the blocker, so the gate
	// sets blocker and NO host meta notice (the reason-repeating "paused …"
	// Progress line was the duplicated-notice bug). The run stays running
	// (restored here after the review_pending probe above): the pause is the
	// blocker record, not a state mutation.
	blocked := run
	blocked.State = "running"
	blocked.BlockerJSON = blockerFixtureJSON(time.Now().UTC(), "need production credentials")
	if err := store.UpdateAutonomyRun(parent, blocked); err != nil {
		t.Fatalf("UpdateAutonomyRun: %v", err)
	}
	g = srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, 1, run, true, newAutonomyWorkMonitor(), &np)
	if g.cont {
		t.Fatal("a recorded blocker must stop continuation")
	}
	if !g.blocker {
		t.Error("blocker stop must be flagged as an explicit report_autonomous_blocker stop")
	}
	if g.notice != "" {
		t.Errorf("blocker stop must not emit a host meta notice duplicating the model's prose, got %q", g.notice)
	}

	// Clearing the blocker (the user's next explicit message does this at
	// request start) lets the chain continue again with the same running run.
	blocked.BlockerJSON = ""
	if err := store.UpdateAutonomyRun(parent, blocked); err != nil {
		t.Fatalf("UpdateAutonomyRun: %v", err)
	}
	np = 0
	if g := srv.evaluateAutonomyContinuation(turnCtx2, "conv-gate", gen2, 1, run, true, newAutonomyWorkMonitor(), &np); !g.cont {
		t.Fatal("a cleared blocker on a still-running run must resume continuation")
	}
}

// TestAutonomyLedgerContentChanged_DecisionsNotProgress pins the durable-content
// predicate: only run identity and state are progress; decisions bookkeeping —
// which the model may be prompted to record every single turn — must never
// count, or the no-progress bound could be reset indefinitely.
func TestAutonomyLedgerContentChanged_DecisionsNotProgress(t *testing.T) {
	prev := conversation.AutonomyRun{RunID: "r1", State: "running", DecisionsJSON: "[]"}

	// Decisions alone: NOT progress.
	cur := prev
	cur.DecisionsJSON = `[{"note":"decided"}]`
	if autonomyLedgerContentChanged(prev, true, cur) {
		t.Error("DecisionsJSON change must not count as ledger progress")
	}

	// State change: progress.
	cur = prev
	cur.State = "review_pending"
	if !autonomyLedgerContentChanged(prev, true, cur) {
		t.Error("state change must count as ledger progress")
	}

	// Run identity change: progress.
	cur = prev
	cur.RunID = "r2"
	if !autonomyLedgerContentChanged(prev, true, cur) {
		t.Error("run identity change must count as ledger progress")
	}

	// First observation of a run during the turn: progress.
	if !autonomyLedgerContentChanged(prev, false, prev) {
		t.Error("first observation of the run must count as ledger progress")
	}

	// Nothing changed: not progress.
	if autonomyLedgerContentChanged(prev, true, prev) {
		t.Error("unchanged ledger row must not count as progress")
	}
}

// TestAutonomyWorkMonitor_ClosedGuard pins the work monitor's closed semantics:
// Reset both clears the count and closes the monitor so late drained events
// cannot resurrect an exhausted segment's evidence (which would silently
// unbound the no-progress streak). The server loop replaces the monitor with a
// fresh one per chain segment; this guards the stale one if it is still
// referenced anywhere.
func TestAutonomyWorkMonitor_ClosedGuard(t *testing.T) {
	m := newAutonomyWorkMonitor()
	m.Observe(runner.Event{Kind: runner.EventToolExecComplete, ToolName: "edit_file"})
	if !m.HasWork() {
		t.Fatal("successful work tool should register work evidence")
	}
	m.Reset()
	if m.HasWork() {
		t.Fatal("Reset must clear the work evidence")
	}
	// Late events after Reset are ignored (closed).
	m.Observe(runner.Event{Kind: runner.EventToolExecComplete, ToolName: "edit_file"})
	if m.HasWork() {
		t.Fatal("events observed after Reset must not resurrect work evidence")
	}
}

// inferenceCapabilities is the shared capability set for the scripted provider
// in these tests.
func inferenceCapabilities() llm.Capabilities {
	return llm.Capabilities{SupportsTools: true}
}

// ledgerCompleter is a test-only tool that flips the run's durable state when
// executed, simulating complete_autonomous_review.
type ledgerCompleter struct {
	store conversation.Store
	runID string
	conv  string
	state string
}

func (c *ledgerCompleter) Name() string                      { return "complete_run" }
func (c *ledgerCompleter) Description() string               { return "completes the run for tests" }
func (c *ledgerCompleter) Permission() agenttools.Permission { return agenttools.PermR }
func (c *ledgerCompleter) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (c *ledgerCompleter) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	if err := c.store.UpdateAutonomyRun(ctx, conversation.AutonomyRun{
		RunID: c.runID, ConversationID: c.conv, State: c.state}); err != nil {
		return nil, err
	}
	return agenttools.NewTextResult("completed"), nil
}

// workingTool is a test-only tool whose successful execution is real
// (non-bookkeeping) work for the work monitor.
type workingTool struct{}

func (w *workingTool) Name() string                      { return "do_work" }
func (w *workingTool) Description() string               { return "does real work for tests" }
func (w *workingTool) Permission() agenttools.Permission { return agenttools.PermR }
func (w *workingTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (w *workingTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	return agenttools.NewTextResult("worked"), nil
}

// failingTool is a test-only tool whose execution always fails, so the work
// monitor must never count it as work evidence.
type failingTool struct{}

func (f *failingTool) Name() string                      { return "failing_work" }
func (f *failingTool) Description() string               { return "always fails for tests" }
func (f *failingTool) Permission() agenttools.Permission { return agenttools.PermR }
func (f *failingTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (f *failingTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	return nil, errors.New("boom: failing_work always fails")
}

// ledgerToucher is a test-only bookkeeping tool that appends to the run's
// DecisionsJSON when executed, simulating capture_decision (whose name is also
// on the autonomy bookkeeping list).
type ledgerToucher struct {
	store conversation.Store
	conv  string
	name  string
}

func (c *ledgerToucher) Name() string                      { return c.name }
func (c *ledgerToucher) Description() string               { return "records a decision for tests" }
func (c *ledgerToucher) Permission() agenttools.Permission { return agenttools.PermR }
func (c *ledgerToucher) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (c *ledgerToucher) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	run, err := c.store.GetActiveAutonomyRun(ctx, c.conv)
	if err != nil {
		return nil, err
	}
	if err := c.store.UpdateAutonomyRun(ctx, conversation.AutonomyRun{
		RunID: run.RunID, ConversationID: c.conv, State: run.State,
		BriefJSON: run.BriefJSON, DecisionsJSON: run.DecisionsJSON + "{}"}); err != nil {
		return nil, err
	}
	return agenttools.NewTextResult("recorded"), nil
}

// blockerReporter is a test-only tool that mirrors the real
// report_autonomous_blocker capability's ledger write: it records a structured
// blocker (reason required) on the active run while leaving the run state
// untouched. Its name matches the real capability so the host's bookkeeping
// classification applies to it as it would in production.
type blockerReporter struct {
	store  conversation.Store
	conv   string
	reason string
}

func (b *blockerReporter) Name() string                      { return "report_autonomous_blocker" }
func (b *blockerReporter) Description() string               { return "reports an autonomous blocker for tests" }
func (b *blockerReporter) Permission() agenttools.Permission  { return agenttools.PermR }
func (b *blockerReporter) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (b *blockerReporter) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	run, err := b.store.GetActiveAutonomyRun(ctx, b.conv)
	if err != nil {
		return nil, err
	}
	run.BlockerJSON = blockerFixtureJSON(time.Now().UTC(), b.reason)
	if err := b.store.UpdateAutonomyRun(ctx, run); err != nil {
		return nil, err
	}
	return agenttools.NewTextResult("blocker recorded"), nil
}

func blockerFixtureJSON(recordedAt time.Time, reason string) string {
	data, _ := json.Marshal(conversation.AutonomyBlocker{Reason: reason, RecordedAt: recordedAt})
	return string(data)
}

// streamCanceler cancels the stream context mid-turn, simulating a client
// disconnecting during an autonomous chain.
type streamCanceler struct {
	cancel context.CancelFunc
}

func (s *streamCanceler) Name() string                      { return "cancel_stream" }
func (s *streamCanceler) Description() string               { return "cancels the stream for tests" }
func (s *streamCanceler) Permission() agenttools.Permission  { return agenttools.PermR }
func (s *streamCanceler) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (s *streamCanceler) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	s.cancel()
	return agenttools.NewTextResult("canceled"), nil
}

// turnSuperseder begins a fresh turn on the conversation mid-turn, simulating a
// new user message arriving while an autonomous chain's turn is in flight, and
// releases it immediately.
type turnSuperseder struct {
	srv  *Server
	conv string
}

func (u *turnSuperseder) Name() string                      { return "supersede_turn" }
func (u *turnSuperseder) Description() string               { return "supersedes the current turn for tests" }
func (u *turnSuperseder) Permission() agenttools.Permission  { return agenttools.PermR }
func (u *turnSuperseder) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (u *turnSuperseder) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	_, _, release := u.srv.beginTurn(context.Background(), u.conv)
	release()
	return agenttools.NewTextResult("superseded"), nil
}

// TestStreamToolLoop_AutonomousBlocker_PausesChain_PreservesApprovals pins the
// end-to-end pause semantics: a running run whose turn records an explicit
// report_autonomous_blocker does NOT chain another turn. The run stays
// "running" (approvals and every other run field preserved), the blocker
// reason is durable, and the model's own final response is the user-facing
// blocker notice — the host adds no duplicate meta lines (see
// TestStreamToolLoop_AutonomousBlocker_NoHostMetaNotices).
func TestStreamToolLoop_AutonomousBlocker_PausesChain_PreservesApprovals(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-blocker", "running")

	reg := agenttools.NewRegistry()
	reg.MustRegister(&blockerReporter{store: store, conv: "conv-blocker", reason: "need production API credentials to finish the deploy"})
	srv.SetToolRegistry(reg)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "t1", ToolName: "report_autonomous_blocker", ToolInput: []byte(`{}`)}},
			{{Type: llm.BlockText, Text: "I am blocked: the deploy needs production API credentials."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}}, // only consumed if the chain (wrongly) continues
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-blocker"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	// Exactly one model turn ran: the blocker pause must stop the chain.
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (no continuation after blocker)", prov.calls)
	}
	if frs := finalResponses(stream.sent); len(frs) != 1 {
		t.Fatalf("FinalResponse count = %d, want 1", len(frs))
	}
	// No host continuation marker may be persisted: nothing auto-restarted.
	turns, err := store.GetTurns(context.Background(), "conv-blocker")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if strings.Contains(fmt.Sprint(turns), autonomyContinuationMarkerPrefix) {
		t.Fatal("a paused run must not persist a host continuation turn")
	}

	// No host meta at all: the model's own final response is the user-facing
	// blocker notice; a host Progress line would only duplicate it.
	notes := progressNotes(stream.sent)
	for _, n := range notes {
		if strings.Contains(n, "autonomous continuation") || strings.Contains(n, run.RunID) ||
			strings.Contains(n, "need production API credentials") {
			t.Errorf("redundant host meta notice on explicit blocker stop: %q", n)
		}
	}

	// Approvals and run identity fully preserved: still running, nothing exited.
	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-blocker")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if cur.RunID != run.RunID || cur.State != "running" {
		t.Fatalf("run must stay running with the same identity, got %q state=%q", cur.RunID, cur.State)
	}
	if cur.BriefJSON != run.BriefJSON || cur.DecisionsJSON != run.DecisionsJSON {
		t.Fatal("brief/decisions must be preserved by the blocker pause")
	}
	blk, ok := cur.ActiveBlocker()
	if !ok || blk.Reason != "need production API credentials to finish the deploy" {
		t.Fatalf("blocker must be durable with the reason, got %+v", blk)
	}
}

// TestStreamToolLoop_AutonomousBlocker_NoHostMetaNotices is the regression for
// the duplicated blocker UI notice: the model's own final response already
// presents the blocker (verification, next steps, the reason), so the host must
// NOT emit redundant unformatted meta lines on top of it. The chain still
// stops, the reason still persists — but no "autonomous continuation ended/
// paused …" Progress notices accompany an explicit report_autonomous_blocker
// stop (unlike genuine errors and the idle-limit safeguard, which keep theirs).
func TestStreamToolLoop_AutonomousBlocker_NoHostMetaNotices(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-blocker-quiet", "running")

	reg := agenttools.NewRegistry()
	reg.MustRegister(&blockerReporter{store: store, conv: "conv-blocker-quiet", reason: "need the user to rotate the deploy token"})
	srv.SetToolRegistry(reg)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "t1", ToolName: "report_autonomous_blocker", ToolInput: []byte(`{}`)}},
			{{Type: llm.BlockText, Text: "Blocked: I need the deploy token rotated. Verification: none run. Next steps: rotate the token, then send any message to resume."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}}, // only consumed if the chain (wrongly) continues
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-blocker-quiet"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	// The blocker still stops the chain: no continuation turn.
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (blocker stops the chain)", prov.calls)
	}
	frs := finalResponses(stream.sent)
	if len(frs) != 1 || frs[0].GetOutput() == "" {
		t.Fatalf("the model's own blocker response must still reach the user, got %v", frs)
	}

	// No host meta at all: neither the generic "autonomous continuation ended:
	// run … is running — waiting for human input" line nor a reason-repeating
	// pause notice. The model's prose is the user-facing blocker notice.
	for _, n := range progressNotes(stream.sent) {
		if strings.Contains(n, "autonomous continuation") ||
			strings.Contains(n, run.RunID) ||
			strings.Contains(n, "need the user to rotate the deploy token") {
			t.Errorf("redundant host meta notice on explicit blocker stop: %q", n)
		}
	}

	// The reason persists durably for resume: the run stays running with the
	// recorded blocker (unchanged pause/resume semantics).
	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-blocker-quiet")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if cur.RunID != run.RunID || cur.State != "running" {
		t.Fatalf("run must stay running with the same identity, got %q state=%q", cur.RunID, cur.State)
	}
	if blk, ok := cur.ActiveBlocker(); !ok || blk.Reason != "need the user to rotate the deploy token" {
		t.Fatalf("blocker reason must persist for resume, got %+v", blk)
	}
}

// TestStreamToolLoop_AutonomousBlocker_ResumesOnNextUserMessage pins the
// resume semantics: the recorded blocker deliberately leaves the run
// "running", and the user's NEXT EXPLICIT MESSAGE is the resume signal — the
// host clears the blocker at request start and chains normally again. Nothing
// auto-restarts the chain while the run sits blocked.
func TestStreamToolLoop_AutonomousBlocker_ResumesOnNextUserMessage(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-resume", "running")

	reg := agenttools.NewRegistry()
	reg.MustRegister(&blockerReporter{store: store, conv: "conv-resume", reason: "waiting on a decision from the user"})
	reg.MustRegister(&ledgerCompleter{store: store, runID: run.RunID, conv: "conv-resume", state: "review_pending"})
	srv.SetToolRegistry(reg)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			// Request 1: blocker turn, then its final response. The chain must stop here.
			{{Type: llm.BlockToolUse, ToolUseID: "t1", ToolName: "report_autonomous_blocker", ToolInput: []byte(`{}`)}},
			{{Type: llm.BlockText, Text: "I am blocked waiting on a decision from the user."}},
			// Request 2 (user's explicit message): the resumed run works and requests exit review.
			{{Type: llm.BlockToolUse, ToolUseID: "t2", ToolName: "complete_run", ToolInput: []byte(`{}`)}},
			{{Type: llm.BlockText, Text: "Unblocked and done: submitting for review."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}}, // must never be consumed
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream1 := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-resume"}, stream1); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls after request 1 = %d, want 2 (chain paused by blocker)", prov.calls)
	}

	// The user's next explicit message: pause cleared, chain resumes with it.
	stream2 := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "here is my decision: use option B", ConversationId: "conv-resume"}, stream2); err != nil {
		t.Fatalf("request 2: %v", err)
	}
	if prov.calls != 4 {
		t.Fatalf("provider calls after request 2 = %d, want 4 (user turn + final; no unexpected extra chain)", prov.calls)
	}
	if frs := finalResponses(stream2.sent); len(frs) != 1 {
		t.Fatalf("request 2 FinalResponse count = %d, want 1", len(frs))
	}

	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-resume")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if _, stillBlocked := cur.ActiveBlocker(); stillBlocked {
		t.Fatal("the user's explicit message must clear the recorded blocker")
	}
	if cur.State != "review_pending" {
		t.Fatalf("run state = %q, want review_pending (resumed run reached its approval handoff)", cur.State)
	}
}

// TestStreamToolLoop_AutonomousBlocker_PendingApprovalNotResumed pins the
// approval-flow preservation: a run parked in review_pending that also carries
// a stale blocker record must NOT be resumed by a user message. The
// review_pending state is the human's exit-review verdict queue — no automatic
// restart, no state mutation, blocker record untouched.
func TestStreamToolLoop_AutonomousBlocker_PendingApprovalNotResumed(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-review", "review_pending")
	// Simulate a stale blocker record surviving alongside the approval state.
	run.BlockerJSON = blockerFixtureJSON(time.Now().UTC(), "stale blocker from before the review handoff")
	if err := store.UpdateAutonomyRun(context.Background(), run); err != nil {
		t.Fatalf("seed stale blocker: %v", err)
	}

	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "The run is waiting for your exit review."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}}, // only consumed if the chain (wrongly) continues
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "status?", ConversationId: "conv-review"}, stream); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (review_pending never chains, even with a stale blocker)", prov.calls)
	}
	if frs := finalResponses(stream.sent); len(frs) != 1 {
		t.Fatalf("FinalResponse count = %d, want 1", len(frs))
	}
	turns, err := store.GetTurns(context.Background(), "conv-review")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if strings.Contains(fmt.Sprint(turns), autonomyContinuationMarkerPrefix) {
		t.Fatal("review_pending must not chain a host continuation turn")
	}

	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-review")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if cur.State != "review_pending" {
		t.Fatalf("approval state must stay review_pending, got %q", cur.State)
	}
	if blk, ok := cur.ActiveBlocker(); !ok || blk.Reason != "stale blocker from before the review handoff" {
		t.Fatalf("stale blocker record must be untouched on a review_pending run, got %+v", blk)
	}
}

// TestStreamToolLoop_AutonomousContinuation_StreamCancellationStopsChain pins
// the cancellation semantics end-to-end: a client stream that goes away mid-run
// stops the chain. The chain only ever rides the open client stream — no
// background relaunch, no persistence of a continuation turn, and the run stays
// durable and resumable.
func TestStreamToolLoop_AutonomousContinuation_StreamCancellationStopsChain(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-cancel", "running")

	sctx, cancel := context.WithCancel(context.Background())
	reg := agenttools.NewRegistry()
	reg.MustRegister(&streamCanceler{cancel: cancel})
	srv.SetToolRegistry(reg)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "t1", ToolName: "cancel_stream", ToolInput: []byte(`{}`)}},
			{{Type: llm.BlockText, Text: "turn one complete."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}}, // only consumed if the chain (wrongly) continues
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: sctx}
	// The stream error (if any) is the transport's business; the chain outcome
	// is what we pin: no continuation turn after cancellation.
	_ = srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-cancel"}, stream)

	if prov.calls > 2 {
		t.Fatalf("provider calls = %d, want <= 2 (canceled stream must stop the chain)", prov.calls)
	}
	turns, err := store.GetTurns(context.Background(), "conv-cancel")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if strings.Contains(fmt.Sprint(turns), "SHOULD NOT RUN") || strings.Contains(fmt.Sprint(turns), autonomyContinuationMarkerPrefix) {
		t.Fatal("canceled chain must not run or persist a continuation turn")
	}
	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-cancel")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if cur.RunID != run.RunID || cur.State != "running" {
		t.Fatalf("run must stay running and resumable after cancellation, got %q state=%q", cur.RunID, cur.State)
	}
}

// TestStreamToolLoop_AutonomousContinuation_NewUserMessageSupersedesChain pins
// the supersession semantics end-to-end: a new user message (a new BeginTurn on
// the conversation) arriving while a chain turn is in flight supersedes the
// chain. No continuation turn runs afterward, the durable run is untouched,
// and the ledger stays resumable by that newer message's own stream.
func TestStreamToolLoop_AutonomousContinuation_NewUserMessageSupersedesChain(t *testing.T) {
	srv, store := newServerWithStore(t)
	run := createAutonomyRun(t, store, "conv-supersede", "running")

	reg := agenttools.NewRegistry()
	reg.MustRegister(&turnSuperseder{srv: srv, conv: "conv-supersede"})
	srv.SetToolRegistry(reg)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "t1", ToolName: "supersede_turn", ToolInput: []byte(`{}`)}},
			{{Type: llm.BlockText, Text: "turn one complete."}},
			{{Type: llm.BlockText, Text: "SHOULD NOT RUN"}}, // only consumed if the chain (wrongly) continues
		},
		caps: inferenceCapabilities(),
	}
	srv.SetCloudLLMProvider(prov)

	stream := &fakeStream{ctx: context.Background()}
	// The superseded turn may error or complete depending on where cancellation
	// lands; either way the chain outcome is what we pin.
	_ = srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "begin", ConversationId: "conv-supersede"}, stream)

	if prov.calls > 2 {
		t.Fatalf("provider calls = %d, want <= 2 (superseded turn must stop the chain)", prov.calls)
	}
	turns, err := store.GetTurns(context.Background(), "conv-supersede")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	if strings.Contains(fmt.Sprint(turns), "SHOULD NOT RUN") || strings.Contains(fmt.Sprint(turns), autonomyContinuationMarkerPrefix) {
		t.Fatal("superseded chain must not run or persist a continuation turn")
	}
	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-supersede")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if cur.RunID != run.RunID || cur.State != "running" {
		t.Fatalf("supersession must not mutate the durable run, got %q state=%q", cur.RunID, cur.State)
	}
}
