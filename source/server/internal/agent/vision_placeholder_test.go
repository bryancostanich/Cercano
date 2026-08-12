package agent

import (
	"encoding/base64"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/visionattach"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestRewriteImagesToPlaceholders_ReplacesAndRegisters(t *testing.T) {
	store := visionattach.NewStore()
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "look at this:"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: b64("PNGBYTES")},
		{Type: llm.BlockText, Text: "and tell me the color"},
	}}}

	out := RewriteImagesToPlaceholders(store, "conv1", msgs)

	blocks := out[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("block count = %d, want 3", len(blocks))
	}
	if blocks[0].Text != "look at this:" || blocks[2].Text != "and tell me the color" {
		t.Errorf("surrounding text not preserved/ordered: %+v", blocks)
	}
	if blocks[1].Type != llm.BlockText {
		t.Fatalf("image block not replaced with text: %+v", blocks[1])
	}
	if !strings.Contains(blocks[1].Text, "inspect_image") || !strings.Contains(blocks[1].Text, "img_") {
		t.Errorf("placeholder missing tool affordance/id: %q", blocks[1].Text)
	}
	// The registered image must be resolvable, and no raw image block remains.
	if store.Count("conv1") != 1 {
		t.Errorf("store count = %d, want 1", store.Count("conv1"))
	}
	for _, b := range blocks {
		if b.Type == llm.BlockImage {
			t.Fatal("a raw image block survived the rewrite")
		}
	}
}

func TestRewriteImagesToPlaceholders_CopyOnWrite(t *testing.T) {
	store := visionattach.NewStore()
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: b64("x")},
	}}}
	_ = RewriteImagesToPlaceholders(store, "c", msgs)
	if msgs[0].Blocks[0].Type != llm.BlockImage {
		t.Error("rewriter mutated the caller's original messages")
	}
}

func TestRewriteImagesToPlaceholders_NilStoreOrBlankConv(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: b64("x")},
	}}}
	if got := RewriteImagesToPlaceholders(nil, "c", msgs); got[0].Blocks[0].Type != llm.BlockImage {
		t.Error("nil store should leave image blocks untouched")
	}
	if got := RewriteImagesToPlaceholders(visionattach.NewStore(), "", msgs); got[0].Blocks[0].Type != llm.BlockImage {
		t.Error("blank conversation id should leave image blocks untouched")
	}
}

func TestRewriteImagesToPlaceholders_URLOnlyImageOmitted(t *testing.T) {
	store := visionattach.NewStore()
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockImage, ImageURL: "https://example/x.png"}, // no inline bytes
	}}}
	out := RewriteImagesToPlaceholders(store, "c", msgs)
	if out[0].Blocks[0].Type != llm.BlockText || !strings.Contains(out[0].Blocks[0].Text, "omitted") {
		t.Errorf("url-only image should become an omitted placeholder: %+v", out[0].Blocks[0])
	}
	if store.Count("c") != 0 {
		t.Errorf("url-only image should not be stored, count=%d", store.Count("c"))
	}
}

func TestRewriteImagesToPlaceholders_CapRejectionOmits(t *testing.T) {
	store := visionattach.NewStore().WithCaps(1, 0)
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: b64("first")},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: b64("second")},
	}}}
	out := RewriteImagesToPlaceholders(store, "c", msgs)
	b := out[0].Blocks
	if !strings.Contains(b[0].Text, "inspect_image") {
		t.Errorf("first image should get a real placeholder: %q", b[0].Text)
	}
	if !strings.Contains(b[1].Text, "omitted") || !strings.Contains(b[1].Text, "limit") {
		t.Errorf("second image should be an omitted/limit placeholder: %q", b[1].Text)
	}
}

func TestRewriteImagesToPlaceholders_NoImagesUnchanged(t *testing.T) {
	store := visionattach.NewStore()
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}
	out := RewriteImagesToPlaceholders(store, "c", msgs)
	if len(out[0].Blocks) != 1 || out[0].Blocks[0].Text != "hi" {
		t.Errorf("text-only message changed: %+v", out[0].Blocks)
	}
}
