package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestHandleImagePasteAttachesChip(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	os.WriteFile(img, onePxPNG, 0o644)

	m := newTestModelWithPrompt() // helper: a Model with a focused promptInput
	handled := m.handleImagePaste(img)
	if !handled {
		t.Fatal("an image path paste should be handled as a drop")
	}
	if !strings.Contains(m.input.Value(), "[image 1]") {
		t.Fatalf("chip not inserted, prompt = %q", m.input.Value())
	}
	if len(m.input.Attachments()) != 1 {
		t.Fatalf("attachment not registered")
	}
}

func TestHandleImagePasteLiteralFallthrough(t *testing.T) {
	m := newTestModelWithPrompt()
	if m.handleImagePaste("just text") {
		t.Fatal("non-image paste must NOT be handled as a drop (so it inserts literally)")
	}
	if m.input.Value() != "" {
		t.Fatalf("literal fallthrough must not modify the prompt here, got %q", m.input.Value())
	}
}

func TestPromptAttachmentsMapToInlineImages(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	os.WriteFile(img, onePxPNG, 0o644)
	m := newTestModelWithPrompt()
	m.input.InsertString("see ")
	m.handleImagePaste(img)

	imgs := promptImagesToInline(m.input.Attachments())
	if len(imgs) != 1 || imgs[0].Index != 1 || imgs[0].MediaType != "image/png" || len(imgs[0].Data) == 0 {
		t.Fatalf("attachment did not map to InlineImage: %+v", imgs)
	}
	_ = agentclient.InlineImage{} // keep import
}

func newTestModelWithPrompt() *Model {
	m := &Model{}
	m.input = newPromptInput()
	m.input.Focus()
	return m
}

// TestQueueCarriesImages verifies that Enqueue→DrainNext preserves the
// attached images on a queued turn (the mid-stream queue carry fix).
func TestQueueCarriesImages(t *testing.T) {
	cv := &chatView{}
	img := agentclient.InlineImage{Index: 1, Data: onePxPNG, MediaType: "image/png"}
	cv.Enqueue("hello", []agentclient.InlineImage{img})

	turn, ok := cv.DrainNext()
	if !ok {
		t.Fatal("DrainNext returned false on a non-empty queue")
	}
	if turn.text != "hello" {
		t.Fatalf("unexpected text: %q", turn.text)
	}
	if len(turn.images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(turn.images))
	}
	if turn.images[0].Index != 1 || turn.images[0].MediaType != "image/png" {
		t.Fatalf("image fields wrong: %+v", turn.images[0])
	}
	if len(turn.images[0].Data) == 0 {
		t.Fatal("image Data is empty")
	}

	// Queue should now be empty.
	if _, ok2 := cv.DrainNext(); ok2 {
		t.Fatal("expected empty queue after drain")
	}
}
