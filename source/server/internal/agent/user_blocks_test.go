package agent

import (
	"encoding/base64"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestBuildUserBlocksNoImages(t *testing.T) {
	blocks := buildUserBlocks("hello world", nil)
	if len(blocks) != 1 || blocks[0].Type != llm.BlockText || blocks[0].Text != "hello world" {
		t.Fatalf("no-image input should be a single text block, got %+v", blocks)
	}
}

func TestBuildUserBlocksInterleaves(t *testing.T) {
	imgs := []InlineImage{{Index: 1, Data: []byte{0x89, 0x50}, MediaType: "image/png"}}
	blocks := buildUserBlocks("look at [image 1] please", imgs)
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks (text, image, text), got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != llm.BlockText || blocks[0].Text != "look at " {
		t.Errorf("block0 wrong: %+v", blocks[0])
	}
	if blocks[1].Type != llm.BlockImage || blocks[1].MediaType != "image/png" ||
		blocks[1].ImageData != base64.StdEncoding.EncodeToString([]byte{0x89, 0x50}) {
		t.Errorf("block1 image wrong: %+v", blocks[1])
	}
	if blocks[2].Type != llm.BlockText || blocks[2].Text != " please" {
		t.Errorf("block2 wrong: %+v", blocks[2])
	}
}

func TestBuildUserBlocksMarkerAtStartAndEnd(t *testing.T) {
	imgs := []InlineImage{{Index: 1, Data: []byte{1}, MediaType: "image/png"}, {Index: 2, Data: []byte{2}, MediaType: "image/gif"}}
	blocks := buildUserBlocks("[image 1][image 2]", imgs)
	if len(blocks) != 2 || blocks[0].Type != llm.BlockImage || blocks[1].Type != llm.BlockImage {
		t.Fatalf("two adjacent markers → two image blocks, got %+v", blocks)
	}
}

func TestBuildUserBlocksUnreferencedImageAppended(t *testing.T) {
	imgs := []InlineImage{{Index: 7, Data: []byte{1}, MediaType: "image/png"}}
	blocks := buildUserBlocks("no marker here", imgs)
	if len(blocks) != 2 || blocks[0].Type != llm.BlockText || blocks[1].Type != llm.BlockImage {
		t.Fatalf("image without a marker should append at end, got %+v", blocks)
	}
}

func TestBuildUserBlocksUnknownMarkerStaysText(t *testing.T) {
	// marker index 9 has no matching image → left as literal text.
	blocks := buildUserBlocks("see [image 9] ok", []InlineImage{{Index: 1, Data: []byte{1}, MediaType: "image/png"}})
	// images present (index 1, no marker) → appended; the [image 9] stays in text.
	var text string
	for _, b := range blocks {
		if b.Type == llm.BlockText {
			text += b.Text
		}
	}
	if text != "see [image 9] ok" {
		t.Fatalf("unknown marker should remain literal text, got %q", text)
	}
}
