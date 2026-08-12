package visioninspect

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/visionattach"
)

// fakeProvider is a controllable inference.Provider for vision tests.
type fakeProvider struct {
	name    string
	resp    llm.ChatResponse
	err     error
	lastReq llm.ChatRequest
	calls   int
	block   chan struct{} // if non-nil, Chat blocks until ctx is done
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsVision: true}
}
func (f *fakeProvider) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	f.calls++
	f.lastReq = req
	if f.block != nil {
		<-ctx.Done()
		return inference.Result{}, ctx.Err()
	}
	if f.err != nil {
		return inference.Result{}, f.err
	}
	return f.resp, nil
}
func (f *fakeProvider) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	return nil, errors.New("not used")
}

func textResp(s string) llm.ChatResponse {
	return llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

// storeWithImage returns a store holding one image for convID and the image id.
func storeWithImage(t *testing.T, convID string) (*visionattach.Store, string) {
	t.Helper()
	s := visionattach.NewStore()
	res := s.Add(convID, "image/png", []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3})
	if res.Rejected || res.Attachment == nil {
		t.Fatalf("store.Add rejected: %+v", res)
	}
	return s, res.Attachment.ID
}

func fixedResolver(p inference.Provider, model string) Resolver {
	return func() (Resolved, bool) {
		if p == nil || model == "" {
			return Resolved{}, false
		}
		return Resolved{Provider: p, Model: model}, true
	}
}

func TestAvailable(t *testing.T) {
	s, _ := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open"}

	if in := New(s, fixedResolver(p, "gemma-3-4b")); !in.Available() {
		t.Fatal("expected available with provider + model")
	}
	if in := New(s, fixedResolver(nil, "")); in.Available() {
		t.Fatal("expected unavailable when resolver misses")
	}
	if in := New(nil, fixedResolver(p, "gemma-3-4b")); in.Available() {
		t.Fatal("expected unavailable with nil store")
	}
	if in := New(s, nil); in.Available() {
		t.Fatal("expected unavailable with nil resolver")
	}
}

func TestLookup(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	in := New(s, fixedResolver(&fakeProvider{}, "m"))
	if !in.Lookup("c1", id) {
		t.Fatal("expected lookup hit for stored image")
	}
	if in.Lookup("c1", "img_bogus_9") {
		t.Fatal("unknown id should miss")
	}
	if in.Lookup("other", id) {
		t.Fatal("cross-conversation lookup should miss")
	}
}

func TestInspect_Success(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open", resp: textResp("A red square on white.")}
	in := New(s, fixedResolver(p, "gemma-3-4b"))

	ans, err := in.Inspect(context.Background(), "c1", id, "  what is in the image?  ")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Answer != "A red square on white." {
		t.Fatalf("answer = %q", ans.Answer)
	}
	if ans.Source != "open:gemma-3-4b" {
		t.Fatalf("source = %q, want open:gemma-3-4b", ans.Source)
	}
}

func TestInspect_ToollessRequest(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open", resp: textResp("answer")}
	in := New(s, fixedResolver(p, "gemma-3-4b"))

	if _, err := in.Inspect(context.Background(), "c1", id, "q?"); err != nil {
		t.Fatal(err)
	}
	// The vision call must expose NO tools — it is a leaf, not an agentic loop.
	if len(p.lastReq.Tools) != 0 {
		t.Fatalf("vision request must carry no tools, got %d", len(p.lastReq.Tools))
	}
	// It must carry the question text and the image block.
	if len(p.lastReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(p.lastReq.Messages))
	}
	var sawText, sawImage bool
	for _, b := range p.lastReq.Messages[0].Blocks {
		switch b.Type {
		case llm.BlockText:
			sawText = b.Text == "q?"
		case llm.BlockImage:
			sawImage = b.MediaType == "image/png" && b.ImageData != ""
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("request must carry the question text and a base64 image block: %+v", p.lastReq.Messages[0].Blocks)
	}
	if p.lastReq.Model != "gemma-3-4b" {
		t.Fatalf("model = %q, want gemma-3-4b", p.lastReq.Model)
	}
}

func TestInspect_StaleImage(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open", resp: textResp("x")}
	in := New(s, fixedResolver(p, "m"))
	s.Clear("c1") // evict between the tool's Lookup and Inspect

	_, err := in.Inspect(context.Background(), "c1", id, "q?")
	if err == nil {
		t.Fatal("expected error for stale image")
	}
	if p.calls != 0 {
		t.Fatalf("stale image must not reach the provider, got %d calls", p.calls)
	}
}

func TestInspect_NoModel(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	in := New(s, fixedResolver(nil, "")) // resolver miss
	_, err := in.Inspect(context.Background(), "c1", id, "q?")
	if err == nil {
		t.Fatal("expected error when no vision model resolves")
	}
}

func TestInspect_ProviderError(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open", err: errors.New("backend down")}
	in := New(s, fixedResolver(p, "m"))
	_, err := in.Inspect(context.Background(), "c1", id, "q?")
	if err == nil || err.Error() != "backend down" {
		t.Fatalf("want backend error, got %v", err)
	}
}

func TestInspect_EmptyAnswer(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open", resp: textResp("   ")}
	in := New(s, fixedResolver(p, "m"))
	_, err := in.Inspect(context.Background(), "c1", id, "q?")
	if err == nil {
		t.Fatal("expected error for empty answer")
	}
}

func TestInspect_Timeout(t *testing.T) {
	s, id := storeWithImage(t, "c1")
	p := &fakeProvider{name: "open", block: make(chan struct{})}
	in := New(s, fixedResolver(p, "m")).WithTimeout(20 * time.Millisecond)

	start := time.Now()
	_, err := in.Inspect(context.Background(), "c1", id, "q?")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("timeout should fire promptly, took %s", time.Since(start))
	}
}

func TestFirstText_JoinsMultipleTextBlocks(t *testing.T) {
	got := firstText([]llm.Block{
		{Type: llm.BlockReasoning, Text: "ignored"},
		{Type: llm.BlockText, Text: "line one"},
		{Type: llm.BlockText, Text: "line two"},
	})
	if got != "line one\nline two" {
		t.Fatalf("firstText = %q", got)
	}
}
