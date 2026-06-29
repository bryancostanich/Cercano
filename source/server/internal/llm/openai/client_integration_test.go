package openai

// Live integration tests against the real OpenAI Chat Completions API.
// Skipped unless INTEGRATION_TEST=1 and OPENAI_API_KEY are set. Model defaults
// to gpt-4o-mini; override with OPENAI_MODEL. Optional OPENAI_BASE_URL points at
// a compatible endpoint (Groq, Gemini-compat, local server, …).
//
//   INTEGRATION_TEST=1 OPENAI_API_KEY=sk-... \
//     go test ./internal/llm/openai/ -run Integration -v

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
)

// redPNGBase64 returns a solid-red 64x64 PNG as base64. Inline bytes work on
// every backend (OpenAI, Gemini-compat, …) unlike image URLs, which some compat
// endpoints refuse to fetch.
func redPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run live OpenAI tests")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return NewClient(Config{BaseURL: os.Getenv("OPENAI_BASE_URL"), APIKey: key, Model: model})
}

func TestIntegration_Chat(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.Chat(ctx, llm.ChatRequest{
		System:   "You are terse. Reply with exactly one word.",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Reply with the single word: pong"}}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var text string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("reply=%q in=%d out=%d", text, resp.InputTokens, resp.OutputTokens)
	if strings.TrimSpace(text) == "" {
		t.Error("empty reply")
	}
	if resp.InputTokens == 0 || resp.OutputTokens == 0 {
		t.Errorf("expected non-zero token usage, got in=%d out=%d", resp.InputTokens, resp.OutputTokens)
	}
}

func TestIntegration_Stream(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rd, err := c.StreamChat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Count from 1 to 5, space separated."}}}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	defer rd.Close()

	var text string
	var sawStop bool
	var in, out int
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventMessageStop:
			sawStop = true
			in, out = ev.InputTokens, ev.OutputTokens
		}
	}
	t.Logf("streamed=%q in=%d out=%d", text, in, out)
	if strings.TrimSpace(text) == "" {
		t.Error("empty streamed text")
	}
	if !sawStop {
		t.Error("never saw EventMessageStop")
	}
	if in == 0 || out == 0 {
		t.Errorf("expected non-zero usage on stop, got in=%d out=%d", in, out)
	}
}

func TestIntegration_ToolCall(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	weather := llm.Tool{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		Schema:      []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
	}
	resp, err := c.Chat(ctx, llm.ChatRequest{
		Tools:    []llm.Tool{weather},
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "What's the weather in Paris? Call the tool."}}}},
	})
	if err != nil {
		t.Fatalf("Chat(tools): %v", err)
	}
	var called bool
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockToolUse {
			called = true
			t.Logf("tool_use name=%q args=%s", b.ToolName, string(b.ToolInput))
			if b.ToolName != "get_weather" {
				t.Errorf("unexpected tool name %q", b.ToolName)
			}
		}
	}
	if !called {
		t.Error("model did not emit a tool_use block")
	}
}

func TestIntegration_Vision(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Inline base64 image — works across OpenAI + compat endpoints (Gemini's
	// compat shim refuses to fetch external image URLs).
	resp, err := c.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "What single color dominates this image? Answer with one word."},
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: redPNGBase64(t)},
		}}},
	})
	if err != nil {
		t.Fatalf("Chat(vision): %v", err)
	}
	var text string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	t.Logf("vision reply=%q", text)
	if strings.TrimSpace(text) == "" {
		t.Error("empty vision reply")
	}
	if !strings.Contains(strings.ToLower(text), "red") {
		t.Logf("note: reply did not mention 'red' (model: %s) — inspect manually", c.model)
	}
}
