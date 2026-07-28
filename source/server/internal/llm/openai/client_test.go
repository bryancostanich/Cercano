package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hello" || resp.InputTokens != 5 || resp.OutputTokens != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if c.Name() != "openai" || !c.Capabilities().SupportsTools || !c.Capabilities().SupportsVision {
		t.Errorf("name/caps wrong")
	}
}

func TestClientChatSerializesExplicitZeroTemperature(t *testing.T) {
	var saw struct {
		Temperature *float64 `json:"temperature"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&saw); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	zero := 0.0
	if _, err := c.Chat(context.Background(), llm.ChatRequest{
		Messages:    []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
		Temperature: &zero,
	}); err != nil {
		t.Fatal(err)
	}
	if saw.Temperature == nil || *saw.Temperature != 0 {
		t.Fatalf("temperature = %#v, want explicit 0", saw.Temperature)
	}
}

func TestNewClientResolvesQuirks(t *testing.T) {
	c := NewClient(Config{Backend: "gemini", Model: "gemini-2.5-flash"})
	if !reflect.DeepEqual(c.quirks, quirksFor("gemini")) {
		t.Errorf("client quirks = %+v, want %+v", c.quirks, quirksFor("gemini"))
	}
}

func TestNewClientDefaultQuirks(t *testing.T) {
	c := NewClient(Config{}) // empty backend → defensive default
	if !c.quirks.ImagesAsBase64 || !c.quirks.NormalizeErrors {
		t.Errorf("empty backend should get defensive quirks, got %+v", c.quirks)
	}
}

// redPNGBytes returns a small solid-red PNG. http.DetectContentType reports
// "image/png" for it.
func redPNGBytes(t *testing.T) []byte {
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

func TestResolveImageURLs(t *testing.T) {
	pngBytes := redPNGBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
	}))
	defer srv.Close()

	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "what color?"},
		{Type: llm.BlockImage, ImageURL: srv.URL},
	}}}

	out, err := resolveImageURLs(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	got := out[0].Blocks[1]
	if got.ImageURL != "" {
		t.Error("ImageURL should be cleared after resolution")
	}
	if got.ImageData == "" {
		t.Error("ImageData should be populated")
	}
	if got.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", got.MediaType)
	}
	// Copy-on-write: the caller's original slice must be untouched.
	if msgs[0].Blocks[1].ImageURL != srv.URL {
		t.Error("resolveImageURLs mutated the caller's messages")
	}
}

func TestResolveImageURLsNoop(t *testing.T) {
	// A base64 block (no URL) and a text block pass through unchanged.
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "hi"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAAA"},
	}}}
	out, err := resolveImageURLs(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, msgs) {
		t.Errorf("no-op changed messages: %+v", out)
	}
}
