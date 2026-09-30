package dispatchhistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func TestRecorder_Begin(t *testing.T) {
	ctx := context.Background()

	// Test with nil sink
	recorder := Begin(ctx, "test-id", nil)
	if recorder != nil {
		t.Error("Expected nil recorder when sink is nil")
	}

	// Test with valid sink
	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		return nil
	}
	recorder = Begin(ctx, "test-id", sink)
	if recorder == nil {
		t.Error("Expected non-nil recorder with valid sink")
	}
}

func TestRecorder_IndependentRecording(t *testing.T) {
	ctx := context.Background()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Ensure conversation exists
	if err := store.EnsureConversation(ctx, "conv1", "/tmp", "test-model"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureConversation(ctx, "conv2", "/tmp", "test-model"); err != nil {
		t.Fatal(err)
	}

	var events1, events2 []conversation.DispatchEvent
	var mu sync.Mutex

	// Create two sinks that collect events
	sink1 := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events1 = append(events1, ev)
		return nil
	}

	sink2 := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events2 = append(events2, ev)
		return nil
	}

	// Begin two independent recorders
	recorder1 := Begin(ctx, "conv1", sink1)
	recorder2 := Begin(ctx, "conv2", sink2)

	// Record events on both
	recorder1.DispatchStart(DispatchStartEvent{
		Task: "task1",
		Mode: "test",
	})

	recorder2.DispatchStart(DispatchStartEvent{
		Task: "task2",
		Mode: "test",
	})

	// Verify events are recorded independently
	if len(events1) != 1 {
		t.Errorf("Expected 1 event for recorder1, got %d", len(events1))
	}
	if len(events2) != 1 {
		t.Errorf("Expected 1 event for recorder2, got %d", len(events2))
	}

	if events1[0].ConversationID != "conv1" {
		t.Errorf("Expected conv1 for recorder1, got %s", events1[0].ConversationID)
	}
	if events2[0].ConversationID != "conv2" {
		t.Errorf("Expected conv2 for recorder2, got %s", events2[0].ConversationID)
	}
}

func TestRecorder_SequenceRetention(t *testing.T) {
	ctx := context.Background()

	var events []conversation.DispatchEvent
	var mu sync.Mutex

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Record a sequence of events
	recorder.DispatchStart(DispatchStartEvent{Task: "test"})
	recorder.ModelRequest(0, "test-provider", llm.ChatRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "user", Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}},
		},
	}, BudgetView{})

	recorder.Compaction(0,
		[]llm.Message{{Role: "user", Blocks: []llm.Block{{Type: llm.BlockText, Text: "old"}}}},
		[]llm.Message{{Role: "user", Blocks: []llm.Block{{Type: llm.BlockText, Text: "new"}}}},
		100)

	recorder.SummarizerRequest(SummarizerRequestEvent{
		Route:  "local",
		Prompt: "summarize",
	})

	recorder.SummarizerResponse(SummarizerResponseEvent{
		Route:  "local",
		Output: "summary",
	})

	recorder.DispatchDone(DispatchDoneEvent{})

	// Verify sequence is retained
	if len(events) != 6 {
		t.Errorf("Expected 6 events, got %d", len(events))
	}

	// Verify sequence numbers are sequential
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Errorf("Sequence numbers not sequential: got %d, %d", events[i-1].Seq, events[i].Seq)
		}
	}

	// Verify specific event types
	expectedTypes := []string{"dispatch_start", "model_request", "compaction", "summarizer_request", "summarizer_response", "dispatch_done"}
	for i, ev := range events {
		if ev.Kind != expectedTypes[i] {
			t.Errorf("Event %d: expected kind %s, got %s", i, expectedTypes[i], ev.Kind)
		}
	}
}

func TestRecorder_OriginalInputsNotMutated(t *testing.T) {
	ctx := context.Background()

	var events []conversation.DispatchEvent
	var mu sync.Mutex

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Create original messages
	originalMessages := []llm.Message{
		{
			Role: "user",
			Blocks: []llm.Block{
				{Type: llm.BlockText, Text: "original text"},
				{Type: llm.BlockImage, ImageData: "fake-image-data"},
			},
		},
	}

	// Record model request
	recorder.ModelRequest(0, "test-provider", llm.ChatRequest{
		Model:    "test-model",
		Messages: originalMessages,
	}, BudgetView{})

	// Verify original messages are not mutated
	if len(originalMessages) != 1 {
		t.Error("Original messages length changed")
	}

	if originalMessages[0].Role != "user" {
		t.Error("Original message role changed")
	}

	if len(originalMessages[0].Blocks) != 2 {
		t.Error("Original blocks count changed")
	}

	if originalMessages[0].Blocks[0].Text != "original text" {
		t.Error("Original text block mutated")
	}

	if originalMessages[0].Blocks[1].ImageData != "fake-image-data" {
		t.Error("Original image data mutated")
	}
}

func TestRecorder_ExcludeImagesAndOpaqueReasoning(t *testing.T) {
	ctx := context.Background()

	var events []conversation.DispatchEvent
	var mu sync.Mutex

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Create messages with images and reasoning
	messages := []llm.Message{
		{
			Role: "user",
			Blocks: []llm.Block{
				{Type: llm.BlockText, Text: "plain text"},
				{Type: llm.BlockImage, ImageData: "secret-image", ImageURL: "http://secret.url"},
				{Type: llm.BlockReasoning, ReasoningData: "opaque-reasoning", ReasoningID: "reasoning-123"},
			},
		},
	}

	// Record model request
	recorder.ModelRequest(0, "test-provider", llm.ChatRequest{
		Model:    "test-model",
		Messages: messages,
	}, BudgetView{})

	// Verify events were recorded
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	// Parse the recorded event
	var event struct {
		Messages []llm.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &event); err != nil {
		t.Fatal(err)
	}

	// Verify text is preserved
	if len(event.Messages) != 1 {
		t.Fatal("Expected 1 message in recorded event")
	}

	if len(event.Messages[0].Blocks) != 3 {
		t.Fatalf("Expected 3 blocks, got %d", len(event.Messages[0].Blocks))
	}

	// Text should be preserved
	if event.Messages[0].Blocks[0].Type != llm.BlockText || event.Messages[0].Blocks[0].Text != "plain text" {
		t.Error("Text block not preserved correctly")
	}

	// Image should be replaced with marker
	if event.Messages[0].Blocks[1].Type != llm.BlockImage ||
		!strings.Contains(event.Messages[0].Blocks[1].Text, "image omitted") {
		t.Error("Image block not replaced with marker")
	}
	if event.Messages[0].Blocks[1].ImageData != "" {
		t.Error("Image data not cleared")
	}
	if event.Messages[0].Blocks[1].ImageURL != "" {
		t.Error("Image URL not cleared")
	}

	// Reasoning should be replaced with marker
	if event.Messages[0].Blocks[2].Type != llm.BlockReasoning ||
		!strings.Contains(event.Messages[0].Blocks[2].Text, "reasoning blob omitted") {
		t.Error("Reasoning block not replaced with marker")
	}
	if event.Messages[0].Blocks[2].ReasoningData != "" {
		t.Error("Reasoning data not cleared")
	}
}

func TestRecorder_ExcludeArbitraryTransportErrorText(t *testing.T) {
	ctx := context.Background()

	var events []conversation.DispatchEvent
	var mu sync.Mutex

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Record model response with arbitrary error
	resp := llm.ChatResponse{
		StopReason: "stop",
		Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "response content"},
		},
	}

	arbitraryError := errors.New("transport error with secret data and URLs")
	recorder.ModelResponse(0, "test-provider", "test-model", resp, arbitraryError)

	// Verify event was recorded
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	// Parse the recorded event
	var event struct {
		Error  string      `json:"error"`
		Blocks []llm.Block `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &event); err != nil {
		t.Fatal(err)
	}

	// Verify error code is sanitized
	if event.Error != "error" {
		t.Errorf("Expected error code 'error', got %q", event.Error)
	}

	// Verify response content is preserved
	if len(event.Blocks) != 1 || event.Blocks[0].Text != "response content" {
		t.Error("Response content not preserved")
	}
}

func TestRecorder_NilAndClosedMethodsSafe(t *testing.T) {
	ctx := context.Background()

	// Test nil recorder
	var nilRecorder *Recorder

	nilRecorder.DispatchStart(DispatchStartEvent{})
	nilRecorder.ModelRequest(0, "", llm.ChatRequest{}, BudgetView{})
	nilRecorder.Compaction(0, nil, nil, 0)
	nilRecorder.SummarizerRequest(SummarizerRequestEvent{})
	nilRecorder.SummarizerResponse(SummarizerResponseEvent{})
	nilRecorder.ToolCall(0, "id", "name", "{}")
	nilRecorder.ToolResult(0, "id", "name", "result", false, 0, false)
	nilRecorder.Note("test", "note")
	nilRecorder.DispatchDone(DispatchDoneEvent{})
	nilRecorder.Close()
	failures := nilRecorder.Failures()
	if failures != 0 {
		t.Errorf("Expected 0 failures for nil recorder, got %d", failures)
	}

	// Test closed recorder
	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		return nil
	}
	recorder := Begin(ctx, "conv1", sink)
	recorder.Close()

	recorder.DispatchStart(DispatchStartEvent{})
	recorder.ModelRequest(0, "", llm.ChatRequest{}, BudgetView{})
	recorder.Compaction(0, nil, nil, 0)
	recorder.SummarizerRequest(SummarizerRequestEvent{})
	recorder.SummarizerResponse(SummarizerResponseEvent{})
	recorder.ToolCall(0, "id", "name", "{}")
	recorder.ToolResult(0, "id", "name", "result", false, 0, false)
	recorder.Note("test", "note")
	recorder.DispatchDone(DispatchDoneEvent{})

	failures = recorder.Failures()
	if failures != 0 {
		t.Errorf("Expected 0 failures for closed recorder, got %d", failures)
	}
}

func TestRecorder_SinkFailureIncrementsFailures(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	failSink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		callCount++
		if callCount == 1 {
			return errors.New("sink failure")
		}
		return nil
	}

	recorder := Begin(ctx, "conv1", failSink)

	// First call should fail
	recorder.DispatchStart(DispatchStartEvent{})
	if recorder.Failures() != 1 {
		t.Errorf("Expected 1 failure, got %d", recorder.Failures())
	}

	// Second call should succeed
	recorder.DispatchStart(DispatchStartEvent{})
	if recorder.Failures() != 1 {
		t.Errorf("Expected 1 failure (no increment on success), got %d", recorder.Failures())
	}
}

func TestRecorder_MetadataOnlyLogOnFailure(t *testing.T) {
	ctx := context.Background()

	var loggedMessages []string
	oldWriter := log.Writer()
	log.SetOutput(&testLogWriter{messages: &loggedMessages})
	defer log.SetOutput(oldWriter)

	failSink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		return errors.New("test failure")
	}

	recorder := Begin(ctx, "conv1", failSink)
	recorder.DispatchStart(DispatchStartEvent{Task: "secret-task"})

	// Verify failure was logged
	if len(loggedMessages) == 0 {
		t.Error("Expected log message on failure")
	}

	logMsg := loggedMessages[0]
	if !strings.Contains(logMsg, "dispatch=conv1 seq=1 kind=dispatch_start") {
		t.Errorf("Unexpected log message: %s", logMsg)
	}
	if strings.Contains(logMsg, "secret-task") {
		t.Error("Log message should not contain secret payload")
	}
}

func TestRecorder_SequenceGapAfterFailure(t *testing.T) {
	var events []conversation.DispatchEvent
	calls := 0
	recorder := Begin(t.Context(), "conv1", func(ctx context.Context, ev conversation.DispatchEvent) error {
		calls++
		if calls == 1 {
			return errors.New("sink failure")
		}
		events = append(events, ev)
		return nil
	})
	recorder.DispatchStart(DispatchStartEvent{})
	recorder.DispatchDone(DispatchDoneEvent{})
	if len(events) != 1 || events[0].Seq != 2 || recorder.Failures() != 1 {
		t.Fatalf("missing visible sequence gap: %+v", events)
	}
}

func TestRecorder_ConcurrentToolResultSerialization(t *testing.T) {
	ctx := context.Background()

	var events []conversation.DispatchEvent
	var mu sync.Mutex

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Launch concurrent tool result calls
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			recorder.ToolResult(0, fmt.Sprintf("tool-id-%d", id), fmt.Sprintf("tool-name-%d", id), fmt.Sprintf("result-%d", id), false, 0, false)
		}(i)
	}
	wg.Wait()

	// Verify all events were recorded with unique sequential IDs
	if len(events) != 10 {
		t.Errorf("Expected 10 events, got %d", len(events))
	}

	// Verify sequence numbers are unique and sequential
	seqs := make(map[int64]bool)
	for _, ev := range events {
		if seqs[ev.Seq] {
			t.Errorf("Duplicate sequence number: %d", ev.Seq)
		}
		seqs[ev.Seq] = true
	}

	// Verify they're sequential
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Errorf("Events not in sequential order")
		}
	}
}

func TestRecorder_CancellationObservesSinkCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []conversation.DispatchEvent
	var mu sync.Mutex

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		// Simulate long-running sink operation
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Start recording
	recorder.DispatchStart(DispatchStartEvent{})

	// Cancel context
	cancel()

	// Try to record another event - should not block indefinitely
	done := make(chan bool)
	go func() {
		recorder.DispatchStart(DispatchStartEvent{})
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("Operation did not complete after cancellation")
	}
}

func TestRecorder_WithRecorderAndFrom(t *testing.T) {
	ctx := context.Background()

	sink := func(ctx context.Context, ev conversation.DispatchEvent) error {
		return nil
	}

	recorder := Begin(ctx, "conv1", sink)

	// Test WithRecorder
	ctx1 := WithRecorder(ctx, recorder)
	retrieved := From(ctx1)
	if retrieved != recorder {
		t.Error("Recorder not properly stored in context")
	}

	// Test From with context without recorder
	ctx2 := context.Background()
	retrieved = From(ctx2)
	if retrieved != nil {
		t.Error("Expected nil recorder from context without recorder")
	}
}

func TestRecorder_Snapshot(t *testing.T) {
	original := []llm.Message{
		{
			Role: "user",
			Blocks: []llm.Block{
				{Type: llm.BlockText, Text: "hello", ToolInput: []byte("args")},
				{Type: llm.BlockImage, ImageData: "image"},
			},
		},
	}

	// Take snapshot
	snapshot := Snapshot(original)

	// Modify original
	original[0].Blocks[0].Text = "modified"
	original[0].Blocks[0].ToolInput = []byte("modified-args")
	original[0].Blocks[1].ImageData = "modified-image"

	// Verify snapshot is unchanged
	if snapshot[0].Blocks[0].Text != "hello" {
		t.Error("Snapshot should not be affected by original modification")
	}
	if string(snapshot[0].Blocks[0].ToolInput) != "args" {
		t.Error("Snapshot tool input should not be affected by original modification")
	}
	if snapshot[0].Blocks[1].ImageData != "image" {
		t.Error("Snapshot image data should not be affected by original modification")
	}
}

// Helper types and functions
type testLogWriter struct {
	messages *[]string
}

func (w *testLogWriter) Write(p []byte) (n int, err error) {
	*w.messages = append(*w.messages, string(p))
	return len(p), nil
}
