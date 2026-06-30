package agent

import (
	"encoding/base64"
	"regexp"
	"strconv"

	"cercano/source/server/internal/llm"
)

// InlineImage is a user-attached image carried alongside the prompt text. The
// prompt text contains "[image <Index>]" markers; buildUserBlocks splices each
// image in at its marker.
type InlineImage struct {
	Index     int
	Data      []byte
	MediaType string
}

var imageMarkerRe = regexp.MustCompile(`\[image (\d+)\]`)

// buildUserBlocks turns a prompt string + inline images into ordered llm blocks:
// text runs interleaved with image blocks at each "[image N]" marker. With no
// images it returns a single text block (preserving prior behavior). A marker
// with no matching image stays literal text; an image with no marker is appended
// at the end so nothing is dropped.
func buildUserBlocks(input string, images []InlineImage) []llm.Block {
	if len(images) == 0 {
		return []llm.Block{{Type: llm.BlockText, Text: input}}
	}
	byIndex := make(map[int]InlineImage, len(images))
	for _, img := range images {
		byIndex[img.Index] = img
	}
	var blocks []llm.Block
	placed := make(map[int]bool)
	last := 0
	for _, m := range imageMarkerRe.FindAllStringSubmatchIndex(input, -1) {
		idx, _ := strconv.Atoi(input[m[2]:m[3]])
		img, ok := byIndex[idx]
		if !ok {
			continue // unknown marker → leave as literal text (folded into next text run)
		}
		if pre := input[last:m[0]]; pre != "" {
			blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: pre})
		}
		blocks = append(blocks, imageBlock(img))
		placed[idx] = true
		last = m[1]
	}
	if tail := input[last:]; tail != "" {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: tail})
	}
	for _, img := range images {
		if !placed[img.Index] {
			blocks = append(blocks, imageBlock(img))
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: ""})
	}
	return blocks
}

func imageBlock(img InlineImage) llm.Block {
	return llm.Block{
		Type:      llm.BlockImage,
		MediaType: img.MediaType,
		ImageData: base64.StdEncoding.EncodeToString(img.Data),
	}
}
