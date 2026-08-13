package agent

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/visionattach"
)

// capturingProvider records the messages of the first StreamChat request and
// then returns a single end-of-turn text reply, so a test can inspect exactly
// what blocks the tool loop sent to the model.
type capturingProvider struct {
	captured []llm.Message
	calls    int
}

func (p *capturingProvider) Name() string { return "capturing" }
func (p *capturingProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *capturingProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *capturingProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if p.calls == 0 {
		p.captured = req.Messages
	}
	p.calls++
	return &scriptedStream{events: blocksToEvents([]llm.Block{{Type: llm.BlockText, Text: "ok"}})}, nil
}

// TestRunToolLoop_RewritesImagesWhenVisionStoreSet proves the Phase 8 wiring:
// when a VisionStore + ConversationID are supplied, the leading user turn's
// image blocks are replaced with an inspect_image placeholder before the model
// is called, and the image is registered in the store so inspect_image can
// resolve it.
func TestRunToolLoop_RewritesImagesWhenVisionStoreSet(t *testing.T) {
	store := visionattach.NewStore()
	prov := &capturingProvider{}

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:       prov,
		Registry:       emptyRegistry(t),
		UserInput:      "what is this?",
		Images:         []InlineImage{{MediaType: "image/png", Data: []byte("PNGDATA")}},
		ConversationID: "conv-1",
		VisionStore:    store,
		MaxIterations:  1,
	})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	// The user turn is the last captured message.
	if len(prov.captured) == 0 {
		t.Fatal("provider captured no messages")
	}
	user := prov.captured[len(prov.captured)-1]

	for _, b := range user.Blocks {
		if b.Type == llm.BlockImage {
			t.Fatalf("raw image block reached the model; expected a placeholder")
		}
	}
	var placeholder string
	for _, b := range user.Blocks {
		if b.Type == llm.BlockText && strings.Contains(b.Text, "inspect_image") {
			placeholder = b.Text
		}
	}
	if placeholder == "" {
		t.Fatalf("no inspect_image placeholder in user blocks: %+v", user.Blocks)
	}
	// The image must be registered so inspect_image can resolve it.
	if got := store.Count("conv-1"); got != 1 {
		t.Fatalf("store.Count = %d, want 1 (image registered)", got)
	}
}

// TestRunToolLoop_LeavesImagesWhenNoVisionStore proves the nil-store default:
// with no VisionStore, image blocks pass through untouched (the provider
// capability gate still strips them for text-only backends).
func TestRunToolLoop_LeavesImagesWhenNoVisionStore(t *testing.T) {
	prov := &capturingProvider{}
	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:       prov,
		Registry:       emptyRegistry(t),
		UserInput:      "what is this?",
		Images:         []InlineImage{{MediaType: "image/png", Data: []byte("PNGDATA")}},
		ConversationID: "conv-1",
		// VisionStore intentionally nil.
		MaxIterations: 1,
	})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	user := prov.captured[len(prov.captured)-1]
	hasImage := false
	for _, b := range user.Blocks {
		if b.Type == llm.BlockImage {
			hasImage = true
		}
	}
	if !hasImage {
		t.Fatal("expected raw image block to pass through when no VisionStore is set")
	}
}
