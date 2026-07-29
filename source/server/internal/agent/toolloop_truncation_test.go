package agent

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// truncatingProvider streams a single tool_use whose input arrives as raw,
// structurally-invalid JSON (the tail was sliced off), then ends the message
// with a caller-chosen StopReason. Its second call answers in text so the loop
// terminates. This reproduces the "large Write cut off at the output cap"
// shape: the collector wraps the invalid input in the malformed envelope, and
// the message stop reason tells the loop *why* it is malformed.
type truncatingProvider struct {
	rawInput   string // invalid JSON delivered as the tool_use input delta
	stopReason string // StopReason on the truncated (first) turn
	calls      int
}

func (p *truncatingProvider) Name() string { return "truncating" }
func (p *truncatingProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *truncatingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *truncatingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	defer func() { p.calls++ }()
	if p.calls == 0 {
		events := []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventToolUseStart, ToolUseID: "w1", ToolName: "Write"},
			{Type: llm.EventToolUseInputDelta, TextDelta: p.rawInput},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, StopReason: p.stopReason},
		}
		return &scriptedStream{events: events}, nil
	}
	events := []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "done"},
		{Type: llm.EventMessageStop, StopReason: "end_turn"},
	}
	return &scriptedStream{events: events}, nil
}

// truncatedInput is a Write call whose arguments were cut off mid-JSON, exactly
// as observed in the CERCANO-MCP incident: {"path": "...mcp_dashboard.go" with
// the content field and closing brace never emitted.
const truncatedInput = `{"path": "/repo/source/clients/cli/internal/ui/mcp_dashboard.go", "content": "package ui\n\nfunc bigThing() {`

func errResultFor(hist []llm.Message, ref string) *llm.Block {
	for i := range hist {
		for j := range hist[i].Blocks {
			b := &hist[i].Blocks[j]
			if b.Type == llm.BlockToolResult && b.ToolUseRef == ref {
				return b
			}
		}
	}
	return nil
}

// (a) A tool call truncated at the output-token cap (StopReason "length") must
// be diagnosed as truncation and told to split the write — NOT told to "resend
// valid JSON", which would loop forever on the same oversized call.
func TestToolLoop_TruncatedToolCall_LengthStop_TellsModelToChunk(t *testing.T) {
	prov := &truncatingProvider{rawInput: truncatedInput, stopReason: llm.StopReasonLength}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		UserInput: "write the dashboard",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := errResultFor(result.History, "w1")
	if res == nil {
		t.Fatal("history: error tool_result for w1 not recorded")
	}
	if !res.IsError {
		t.Error("tool_result must be flagged as error")
	}
	if strings.Contains(res.Content, "not valid JSON") {
		t.Errorf("truncation must NOT be reported as invalid JSON, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "cut off") || !strings.Contains(res.Content, "output-token limit") {
		t.Errorf("tool_result should name the truncation, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "smaller") {
		t.Errorf("tool_result should tell the model to split into smaller calls, got: %q", res.Content)
	}
}

// (b) The same shape of malformed input, but the response finished normally
// (StopReason "stop") — a genuine authoring mistake, not a size limit. This
// must still get the original "resend valid JSON" guidance (no regression).
func TestToolLoop_MalformedToolCall_NonLengthStop_KeepsInvalidJSONMessage(t *testing.T) {
	prov := &truncatingProvider{rawInput: truncatedInput, stopReason: "stop"}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		UserInput: "write the dashboard",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := errResultFor(result.History, "w1")
	if res == nil {
		t.Fatal("history: error tool_result for w1 not recorded")
	}
	if !strings.Contains(res.Content, "not valid JSON") {
		t.Errorf("non-truncation malformed input should keep the invalid-JSON message, got: %q", res.Content)
	}
	if strings.Contains(res.Content, "cut off") {
		t.Errorf("a non-length stop must not be diagnosed as truncation, got: %q", res.Content)
	}
}
