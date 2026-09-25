package dispatchhistory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// GLM-style chat_completions reasoning rides the turn as plaintext in
// ReasoningData (see llm/openai/stream.go). By default it is redacted to a byte
// count, which is correct for normal operation but leaves nothing to read when
// diagnosing why a dispatch behaved the way it did.

func reasoningResponse() llm.ChatResponse {
	return llm.ChatResponse{
		StopReason: "tool_use",
		Blocks: []llm.Block{
			{Type: llm.BlockReasoning, ReasoningData: "The file is 405KB. I need to page through it."},
			{Type: llm.BlockToolUse, ToolName: "Read", ToolInput: json.RawMessage(`{"path":"job.rs"}`)},
		},
	}
}

// captureRecorder returns a Recorder writing into a slice of events.
func captureRecorder(t *testing.T, events *[]conversation.DispatchEvent) *Recorder {
	t.Helper()
	var mu sync.Mutex
	rec := Begin(context.Background(), "reasoning-test", func(_ context.Context, ev conversation.DispatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		*events = append(*events, ev)
		return nil
	})
	if rec == nil {
		t.Fatal("recorder not created")
	}
	return rec
}

func recordedBlocks(t *testing.T, events []conversation.DispatchEvent) []llm.Block {
	t.Helper()
	for _, ev := range events {
		if ev.Kind != "model_response" {
			continue
		}
		var decoded struct {
			Blocks []llm.Block `json:"blocks"`
		}
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded.Blocks
	}
	t.Fatal("no model_response event recorded")
	return nil
}

func TestReasoningRedactedByDefault(t *testing.T) {
	var events []conversation.DispatchEvent
	rec := captureRecorder(t, &events)
	rec.ModelResponse(1, "deepinfra", "glm", reasoningResponse(), nil)

	blocks := recordedBlocks(t, events)
	if blocks[0].ReasoningData != "" {
		t.Fatalf("reasoning text stored without opt-in: %q", blocks[0].ReasoningData)
	}
	if !strings.Contains(blocks[0].Text, "omitted") {
		t.Fatalf("expected an omission marker, got %q", blocks[0].Text)
	}
}

func TestReasoningCapturedWhenEnabled(t *testing.T) {
	var events []conversation.DispatchEvent
	rec := captureRecorder(t, &events)
	rec.CaptureReasoning(true)
	rec.ModelResponse(1, "deepinfra", "glm", reasoningResponse(), nil)

	blocks := recordedBlocks(t, events)
	if !strings.Contains(blocks[0].ReasoningData, "405KB") {
		t.Fatalf("reasoning not captured: %q", blocks[0].ReasoningData)
	}
	// Other block kinds must be unaffected by the opt-in.
	if blocks[1].ToolName != "Read" {
		t.Fatalf("tool block damaged: %+v", blocks[1])
	}
}

// Capture must be bounded: a single reasoning block in the reproduction ran to
// 15,268 characters, and storing them unbounded would bloat the database.
func TestCapturedReasoningIsBounded(t *testing.T) {
	var events []conversation.DispatchEvent
	rec := captureRecorder(t, &events)
	rec.CaptureReasoning(true)

	huge := strings.Repeat("z", maxCapturedReasoning*3)
	rec.ModelResponse(1, "deepinfra", "glm", llm.ChatResponse{
		Blocks: []llm.Block{{Type: llm.BlockReasoning, ReasoningData: huge}},
	}, nil)

	got := recordedBlocks(t, events)[0].ReasoningData
	if len(got) > maxCapturedReasoning+64 {
		t.Fatalf("captured %d bytes, want <= %d plus a truncation note", len(got), maxCapturedReasoning)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("truncation not disclosed: %q", got[len(got)-80:])
	}
}

// Capture must not mutate the caller's response: the same blocks are sent back
// to the provider on the continuation, and corrupting them would change
// behavior rather than observe it.
func TestCaptureDoesNotMutateCallerBlocks(t *testing.T) {
	var events []conversation.DispatchEvent
	rec := captureRecorder(t, &events)
	rec.CaptureReasoning(true)

	resp := reasoningResponse()
	original := resp.Blocks[0].ReasoningData
	rec.ModelResponse(1, "deepinfra", "glm", resp, nil)

	if resp.Blocks[0].ReasoningData != original {
		t.Fatalf("caller's reasoning mutated: %q", resp.Blocks[0].ReasoningData)
	}
}

// Opaque Responses-API blobs are encrypted round-trip state, not readable
// thought. Capturing them stores bulk with no diagnostic value.
func TestOpaqueResponsesBlobStaysRedacted(t *testing.T) {
	var events []conversation.DispatchEvent
	rec := captureRecorder(t, &events)
	rec.CaptureReasoning(true)

	rec.ModelResponse(1, "openai", "gpt", llm.ChatResponse{
		Blocks: []llm.Block{{
			Type:          llm.BlockReasoning,
			ReasoningID:   "rs_abc123",
			ReasoningData: "gAAAAABn0pQ-encrypted-blob",
		}},
	}, nil)

	got := recordedBlocks(t, events)[0]
	if got.ReasoningData != "" {
		t.Fatalf("opaque blob captured: %q", got.ReasoningData)
	}
	if !strings.Contains(got.Text, "omitted") {
		t.Fatalf("expected omission marker for opaque reasoning, got %q", got.Text)
	}
}
