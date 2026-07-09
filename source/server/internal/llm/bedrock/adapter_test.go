package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

func redPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocumentRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"city":"Paris","n":3}`)
	got := documentToJSON(jsonToDocument(raw))
	// numbers must survive (n stays 3, not "3")
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", got, err)
	}
	if m["city"] != "Paris" || m["n"].(float64) != 3 {
		t.Fatalf("round-trip lost data: %s", got)
	}
}

func TestMessagesToConverse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(redPNG(t))
	}))
	defer srv.Close()

	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "hi"},
			{Type: llm.BlockImage, ImageURL: srv.URL},
		}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockToolUse, ToolUseID: "call_1", ToolName: "get_weather", ToolInput: json.RawMessage(`{"city":"Paris"}`)},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "call_1", Content: "sunny"},
		}},
	}
	out, err := messagesToConverse(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 messages, got %d", len(out))
	}
	// msg0: user with text + image
	if out[0].Role != types.ConversationRoleUser || len(out[0].Content) != 2 {
		t.Fatalf("msg0 = %+v", out[0])
	}
	if _, ok := out[0].Content[0].(*types.ContentBlockMemberText); !ok {
		t.Errorf("msg0.0 not text: %T", out[0].Content[0])
	}
	img, ok := out[0].Content[1].(*types.ContentBlockMemberImage)
	if !ok || img.Value.Format != types.ImageFormatPng {
		t.Errorf("msg0.1 image wrong: %T fmt=%v", out[0].Content[1], img)
	}
	if bs, ok := img.Value.Source.(*types.ImageSourceMemberBytes); !ok || len(bs.Value) == 0 {
		t.Errorf("image bytes missing")
	}
	// msg1: assistant tool use
	tu, ok := out[1].Content[0].(*types.ContentBlockMemberToolUse)
	if !ok || aws.ToString(tu.Value.ToolUseId) != "call_1" || aws.ToString(tu.Value.Name) != "get_weather" {
		t.Errorf("msg1 tool use wrong: %+v", out[1].Content[0])
	}
	// msg2: user tool result
	tr, ok := out[2].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok || aws.ToString(tr.Value.ToolUseId) != "call_1" || tr.Value.Status != types.ToolResultStatusSuccess {
		t.Errorf("msg2 tool result wrong: %+v", out[2].Content[0])
	}
}

func TestToolsAndSystemAndInference(t *testing.T) {
	tc := toolsToConverse([]llm.Tool{{Name: "get_weather", Description: "w", Schema: json.RawMessage(`{"type":"object"}`)}})
	if tc == nil || len(tc.Tools) != 1 {
		t.Fatalf("tool config = %+v", tc)
	}
	if ts, ok := tc.Tools[0].(*types.ToolMemberToolSpec); !ok || aws.ToString(ts.Value.Name) != "get_weather" {
		t.Errorf("toolspec wrong: %+v", tc.Tools[0])
	}
	if toolsToConverse(nil) != nil {
		t.Error("nil tools should map to nil config")
	}
	if systemBlocks("") != nil {
		t.Error("empty system should be nil")
	}
	if sb := systemBlocks("sys"); len(sb) != 1 {
		t.Errorf("system blocks = %+v", sb)
	}
	if inferenceConfig(llm.ChatRequest{}) != nil {
		t.Error("no max/temp → nil inference config")
	}
	ic := inferenceConfig(llm.ChatRequest{MaxTokens: 100})
	if ic == nil || aws.ToInt32(ic.MaxTokens) != 100 {
		t.Errorf("inference config = %+v", ic)
	}
}

func TestBlocksFromConverse(t *testing.T) {
	m := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "hello"},
			&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: aws.String("call_1"), Name: aws.String("get_weather"),
				Input: jsonToDocument(json.RawMessage(`{"city":"Paris"}`)),
			}},
		},
	}
	blocks := blocksFromConverse(m)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != llm.BlockText || blocks[0].Text != "hello" {
		t.Errorf("block0 = %+v", blocks[0])
	}
	if blocks[1].Type != llm.BlockToolUse || blocks[1].ToolUseID != "call_1" || blocks[1].ToolName != "get_weather" {
		t.Errorf("block1 = %+v", blocks[1])
	}
	var in map[string]any
	if err := json.Unmarshal(blocks[1].ToolInput, &in); err != nil || in["city"] != "Paris" {
		t.Errorf("block1 input = %s (err %v)", blocks[1].ToolInput, err)
	}
}

// Foreign blocks (e.g. reasoning recorded on the Responses backend) have no
// Converse representation and are skipped by the block switch. A message left
// with no content must be dropped whole — Converse rejects empty content with
// a ValidationException.
func TestMessagesToConverse_DropsMessagesLeftEmpty(t *testing.T) {
	out, err := messagesToConverse(context.Background(), []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, ReasoningID: "rs_1", ReasoningData: "gAAAAA"},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "next"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected reasoning-only message dropped, got %d: %+v", len(out), out)
	}
	if out[0].Role != types.ConversationRoleUser {
		t.Errorf("surviving message wrong: %+v", out[0])
	}
}
