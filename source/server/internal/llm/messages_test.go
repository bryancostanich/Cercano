package llm

import (
	"encoding/json"
	"testing"
)

func TestBlock_RoundTrip_Text(t *testing.T) {
	in := Block{Type: BlockText, Text: "hello"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Block
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != BlockText || out.Text != "hello" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestBlock_RoundTrip_ToolUse(t *testing.T) {
	in := Block{
		Type:      BlockToolUse,
		ToolUseID: "toolu_01",
		ToolName:  "read_file",
		ToolInput: json.RawMessage(`{"path":"main.go"}`),
	}
	b, _ := json.Marshal(in)
	var out Block
	_ = json.Unmarshal(b, &out)
	if out.ToolName != "read_file" || string(out.ToolInput) != `{"path":"main.go"}` {
		t.Errorf("tool_use round-trip mismatch: %+v", out)
	}
}

func TestBlock_RoundTrip_ToolResult(t *testing.T) {
	in := Block{
		Type:       BlockToolResult,
		ToolUseRef: "toolu_01",
		Content:    "32 lines",
		IsError:    false,
	}
	b, _ := json.Marshal(in)
	var out Block
	_ = json.Unmarshal(b, &out)
	if out.ToolUseRef != "toolu_01" || out.Content != "32 lines" {
		t.Errorf("tool_result round-trip mismatch: %+v", out)
	}
}

func TestBlock_RoundTrip_Image_Base64(t *testing.T) {
	in := Block{
		Type:      BlockImage,
		MediaType: "image/png",
		ImageData: "QUJD",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Block
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != BlockImage || out.MediaType != "image/png" || out.ImageData != "QUJD" {
		t.Errorf("image base64 round-trip mismatch: %+v", out)
	}
}

func TestBlock_RoundTrip_Image_URL(t *testing.T) {
	in := Block{
		Type:     BlockImage,
		ImageURL: "https://example.com/img.png",
	}
	b, _ := json.Marshal(in)
	var out Block
	_ = json.Unmarshal(b, &out)
	if out.Type != BlockImage || out.ImageURL != "https://example.com/img.png" {
		t.Errorf("image url round-trip mismatch: %+v", out)
	}
}

func TestMessage_OrderedBlocks(t *testing.T) {
	m := Message{Role: RoleAssistant, Blocks: []Block{
		{Type: BlockText, Text: "I'll read it."},
		{Type: BlockToolUse, ToolUseID: "u1", ToolName: "read_file"},
	}}
	if len(m.Blocks) != 2 || m.Blocks[1].ToolName != "read_file" {
		t.Errorf("message blocks not preserved: %+v", m)
	}
}

func TestBlockReasoningRoundTrip(t *testing.T) {
	b := Block{Type: BlockReasoning, ReasoningID: "rs_1", ReasoningData: "ENCRYPTED"}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var got Block
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != BlockReasoning || got.ReasoningID != "rs_1" || got.ReasoningData != "ENCRYPTED" {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}
