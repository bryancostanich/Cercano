# Image Input (drop / paste images into the CLI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user attach images to a prompt in `cercano-cli` by dropping an image file onto the terminal or pasting a copied image (Cmd+V); the image shows as an atomic `[image N]` chip and travels with the turn to the model, interleaved at the chip's position.

**Architecture:** A terminal "drop" is a paste of the file path, so capture is paste-interception (detect image paths) plus an OS-clipboard peek for copied-image bytes. The custom prompt widget grows an append-only image registry; `[image N]` markers in the text are the source of truth (submit gathers only images whose marker survives). Images travel as bytes in the gRPC request; the server splits the text on markers and interleaves text + image content blocks. The LLM wire layer already serializes image blocks.

**Tech Stack:** Go; two modules (`source/server`, `source/clients/cli`); gRPC/protobuf (`protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`); Bubble Tea v2 (`charm.land/bubbletea/v2`), Lipgloss v2; macOS `osascript`/`pngpaste` for clipboard image.

## Global Constraints

- Two Go modules. Server: `cd source/server`; build `go build ./...`, test `go test ./... -count=1`. CLI: `cd source/clients/cli`; build `go build -o bin/cercano-cli .`, test `go test ./... -count=1`.
- Image bytes travel in the request (not file paths) — one path for both file-drop and clipboard. Keys/secrets are unaffected.
- Marker format is exactly `[image N]` where `N` is the attachment's integer id (e.g. `[image 1]`). The same regex governs both ends: `\[image (\d+)\]`.
- Accepted image types: PNG, JPEG/JPG, GIF, WEBP. Per-image cap **20 MiB** (mirror server `maxImageBytes` = `20 << 20`, `source/server/internal/llm/image.go:12`). Oversized or non-image → not attached.
- `internal/form` must not import `internal/ui` (unrelated here, but the no-cycle rule stands).
- Vision capability: **warn but allow** — never block an image at the client based on capability (leaves room for future capability-aware routing).
- Clipboard image is macOS-first; other platforms return "unsupported" (file drop still works everywhere).
- Commit messages must not contain the word "Claude". Do not `git push`.

---

### Task 1: Proto — InlineImage message + images field

**Files:**
- Modify: `source/proto/agent.proto` (the `ProcessRequestRequest` message ~line 158-167; add a new message near it)
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `agent_grpc.pb.go`

**Interfaces:**
- Produces: `proto.InlineImage{ Index int32; Data []byte; MediaType string }` with accessors `GetIndex()/GetData()/GetMediaType()`; `ProcessRequestRequest` gains `repeated InlineImage images = 9` with `GetImages()`.

- [ ] **Step 1: Edit the proto**

In `source/proto/agent.proto`, add the new message immediately after `ProcessRequestRequest` and add the field inside `ProcessRequestRequest` (field number `9` — current fields are 1,3,4,5,6,7,8; 2 was removed):

```proto
message ProcessRequestRequest {
  string input = 1;
  string work_dir = 3;
  string file_name = 4;
  string conversation_id = 5;
  bool direct_local = 6;
  string model_override = 7;
  bool coproc = 8;
  repeated InlineImage images = 9; // user-attached images, spliced in at "[image N]" markers in input
}

// InlineImage is one user-attached image. Index matches the "[image <Index>]"
// marker in ProcessRequestRequest.input; data is the raw image bytes.
message InlineImage {
  int32 index = 1;
  bytes data = 2;
  string media_type = 3; // e.g. "image/png"
}
```

(Keep the existing comments on the other fields; only `input` etc. are shown condensed here — do not delete the field-2-removed comment if present.)

- [ ] **Step 2: Install codegen plugins (if missing) and regenerate**

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$PATH:$(go env GOPATH)/bin"
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
protoc -I source/proto \
  --go_out=source/server --go_opt=module=cercano/source/server \
  --go-grpc_out=source/server --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

- [ ] **Step 3: Verify the generated surface compiles**

```bash
cd source/server && go build ./...
grep -c "type InlineImage struct\|func (x \*InlineImage) GetData\|func (x \*ProcessRequestRequest) GetImages" pkg/proto/agent.pb.go
```
Expected: build clean; grep count ≥ 3. Then `go test ./... -count=1` (the mcp `mockAgentClient` implements `proto.AgentClient`; adding a message/field does NOT change interface methods, so it should still compile — confirm).

- [ ] **Step 4: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/proto/agent.proto source/server/pkg/proto/agent.pb.go source/server/pkg/proto/agent_grpc.pb.go
git commit -m "feat(proto): InlineImage + images field on ProcessRequestRequest"
```

---

### Task 2: Server — buildUserBlocks + image plumbing

**Files:**
- Create: `source/server/internal/agent/user_blocks.go`
- Create: `source/server/internal/agent/user_blocks_test.go`
- Modify: `source/server/internal/agent/router.go` (`Request` struct ~line 64-76)
- Modify: `source/server/internal/agent/toolloop.go` (`ToolLoopInput` struct ~line 35-50; user-message build ~line 126-129)
- Modify: `source/server/internal/agent/llm_adapter.go` (`Process` user message ~line 31-39)
- Modify: `source/server/internal/server/server.go` (`mapRequest` ~line 1752; the `ToolLoopInput` build at `UserInput: req.GetInput()` ~line 1659)

**Interfaces:**
- Produces: `agent.InlineImage{ Index int; Data []byte; MediaType string }`; `agent.buildUserBlocks(input string, images []InlineImage) []llm.Block`; `agent.Request.Images []InlineImage`; `agent.ToolLoopInput.Images []InlineImage`.
- Consumes: `proto.InlineImage` (Task 1); `llm.Block`, `llm.BlockText`, `llm.BlockImage` (`internal/llm/messages.go`).

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/agent/user_blocks_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd source/server && go test ./internal/agent/ -run BuildUserBlocks -count=1
```
Expected: FAIL — `buildUserBlocks` / `InlineImage` undefined.

- [ ] **Step 3: Implement buildUserBlocks**

Create `source/server/internal/agent/user_blocks.go`:

```go
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
```

- [ ] **Step 4: Run to verify it passes**

```bash
cd source/server && go test ./internal/agent/ -run BuildUserBlocks -count=1
```
Expected: PASS.

- [ ] **Step 5: Add Images to the structs and wire the two build sites**

In `source/server/internal/agent/router.go`, add to `Request` (after `Coproc bool`, ~line 71):

```go
	// Images are user-attached images; buildUserBlocks splices them into the
	// user message at "[image N]" markers in Input.
	Images []InlineImage
```

In `source/server/internal/agent/toolloop.go`, add to `ToolLoopInput` (after `UserInput string`, ~line 40):

```go
	Images      []InlineImage
```

Replace the user-message build in `toolloop.go` (~line 126-129):

```go
	hist = append(hist, llm.Message{
		Role:   llm.RoleUser,
		Blocks: buildUserBlocks(in.UserInput, in.Images),
	})
```

Replace the user message in `llm_adapter.go` `Process` (~line 33-38):

```go
		Messages: []llm.Message{
			{
				Role:   llm.RoleUser,
				Blocks: buildUserBlocks(req.Input, req.Images),
			},
		},
```

- [ ] **Step 6: Map proto images → agent at both server call sites**

In `source/server/internal/server/server.go`, add a helper (place near `mapRequest`):

```go
// mapInlineImages converts proto images to agent.InlineImage.
func mapInlineImages(in []*proto.InlineImage) []agent.InlineImage {
	if len(in) == 0 {
		return nil
	}
	out := make([]agent.InlineImage, 0, len(in))
	for _, p := range in {
		out = append(out, agent.InlineImage{
			Index:     int(p.GetIndex()),
			Data:      p.GetData(),
			MediaType: p.GetMediaType(),
		})
	}
	return out
}
```

In `mapRequest` (~line 1753), add `Images: mapInlineImages(req.Images),` to the returned `&agent.Request{...}`.

At the `ToolLoopInput` build (~line 1659, the line `UserInput: req.GetInput(),`), add immediately after it:

```go
		Images:              mapInlineImages(req.GetImages()),
```

- [ ] **Step 7: Build + test the server module**

```bash
cd source/server && go build ./... && go test ./... -count=1
```
Expected: clean + green.

- [ ] **Step 8: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/server/internal/agent/ source/server/internal/server/server.go
git commit -m "feat(agent): carry user images into the LLM message via buildUserBlocks"
```

---

### Task 3: agentclient — thread images through StreamChat

**Files:**
- Modify: `source/server/pkg/agentclient/client.go` (`StreamChat` ~line 914-919)

**Interfaces:**
- Produces: `agentclient.InlineImage{ Index int32; Data []byte; MediaType string }`; `StreamChat(ctx, conversationID, input, workDir string, images ...InlineImage) (<-chan StreamMsg, error)` — variadic so existing callers keep compiling.

- [ ] **Step 1: Add the type and thread the param**

In `source/server/pkg/agentclient/client.go`, add near the top-level types:

```go
// InlineImage is a user-attached image sent with a chat turn. Index matches the
// "[image <Index>]" marker in the input text.
type InlineImage struct {
	Index     int32
	Data      []byte
	MediaType string
}
```

Change `StreamChat`'s signature and the request it builds (~line 914):

```go
func (c *Client) StreamChat(ctx context.Context, conversationID, input, workDir string, images ...InlineImage) (<-chan StreamMsg, error) {
	stream, err := c.agent.StreamProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:          input,
		ConversationId: conversationID,
		WorkDir:        workDir,
		Images:         toProtoImages(images),
	})
```

Add the mapper next to `StreamChat`:

```go
func toProtoImages(images []InlineImage) []*proto.InlineImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]*proto.InlineImage, 0, len(images))
	for _, img := range images {
		out = append(out, &proto.InlineImage{
			Index:     img.Index,
			Data:      img.Data,
			MediaType: img.MediaType,
		})
	}
	return out
}
```

- [ ] **Step 2: Build both modules (variadic keeps existing CLI caller compiling)**

```bash
cd source/server && go build ./... && go test ./... -count=1
cd ../clients/cli && go build ./... && go test ./... -count=1
```
Expected: all clean/green (the existing `d.agent.StreamChat(ctx, convID, input, workDir)` call in `main_agent_driver.go` still compiles — zero images).

- [ ] **Step 3: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/server/pkg/agentclient/client.go
git commit -m "feat(agentclient): optional images on StreamChat"
```

---

### Task 4: prompt widget — image registry + atomic chip

**Files:**
- Create: `source/clients/cli/internal/ui/prompt_image.go`
- Create: `source/clients/cli/internal/ui/prompt_image_test.go`
- Modify: `source/clients/cli/internal/ui/prompt_input.go` (`promptInput` struct ~line 24-49; `SetValue` ~line 111-120; `deleteBackward` ~line 506-518; `deleteForward` ~line 520-531)

**Interfaces:**
- Produces on `promptInput`: `AddImage(data []byte, mediaType, source string)`; `Attachments() []promptImage` (live, in marker order); fields `attachments []promptImage`, `nextImageID int`; type `promptImage{ id int; data []byte; mediaType, source string }`. Atomic backspace/delete over `[image N]` spans whose id is live.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing test**

Create `source/clients/cli/internal/ui/prompt_image_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAddImageInsertsMarkerAndRegisters(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1, 2, 3}, "image/png", "/tmp/a.png")
	if !strings.Contains(p.Value(), "[image 1]") {
		t.Fatalf("marker not inserted: %q", p.Value())
	}
	att := p.Attachments()
	if len(att) != 1 || att[0].id != 1 || att[0].mediaType != "image/png" {
		t.Fatalf("attachment not registered: %+v", att)
	}
}

func TestBackspaceDeletesWholeChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.InsertString("hi ")
	p.AddImage([]byte{1}, "image/png", "")
	// cursor is right after "[image 1]". One backspace removes the whole chip.
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if strings.Contains(p.Value(), "image") {
		t.Fatalf("chip not deleted atomically: %q", p.Value())
	}
	if p.Value() != "hi " {
		t.Fatalf("want \"hi \" after deleting chip, got %q", p.Value())
	}
	if len(p.Attachments()) != 0 {
		t.Fatalf("attachment should drop out once its marker is gone: %+v", p.Attachments())
	}
}

func TestDeleteForwardDeletesWholeChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")
	p.InsertString(" tail")
	p.CursorStart() // cursor before "[image 1]"
	p.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	if p.Value() != " tail" {
		t.Fatalf("delete-forward should remove whole chip, got %q", p.Value())
	}
}

func TestAttachmentsFollowMarkers(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")  // [image 1]
	p.AddImage([]byte{2}, "image/gif", "")  // [image 2]
	if len(p.Attachments()) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(p.Attachments()))
	}
	// Deleting one marker's text drops only that attachment from Attachments().
	p.SetValue("[image 2]") // simulate text where only marker 2 survives
	att := p.Attachments()
	if len(att) != 1 || att[0].id != 2 {
		t.Fatalf("Attachments must follow surviving markers, got %+v", att)
	}
}

func TestResetClearsAttachments(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")
	p.Reset()
	if len(p.Attachments()) != 0 || len(p.attachments) != 0 {
		t.Fatalf("Reset must clear attachments")
	}
}
```

Note: `SetValue` clears the registry (see Step 3), so `TestAttachmentsFollowMarkers` would lose the attachments. To test marker-following without losing the registry, that test must instead delete via editing rather than SetValue. Replace that test body with an edit-based deletion:

```go
func TestAttachmentsFollowMarkers(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "") // [image 1]
	p.AddImage([]byte{2}, "image/gif", "") // [image 2]
	if len(p.Attachments()) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(p.Attachments()))
	}
	// Backspace removes the trailing chip ([image 2]); attachment 2 drops out.
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	att := p.Attachments()
	if len(att) != 1 || att[0].id != 1 {
		t.Fatalf("Attachments must follow surviving markers, got %+v", att)
	}
}
```

(Use this edit-based version; delete the SetValue-based snippet above.)

- [ ] **Step 2: Run to verify it fails**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'AddImage|Chip|Attachments|ResetClears' -count=1
```
Expected: FAIL — `AddImage`/`Attachments`/`promptImage` undefined.

- [ ] **Step 3: Implement the registry + spans**

Create `source/clients/cli/internal/ui/prompt_image.go`:

```go
package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// promptImage is one image attached to the prompt. The prompt text carries an
// "[image <id>]" marker; the registry is append-only per prompt and Attachments()
// returns only those whose marker still appears in the text.
type promptImage struct {
	id        int
	data      []byte
	mediaType string
	source    string // file path, or "" for clipboard
}

type imageSpan struct {
	start int // rune index of '['
	end   int // rune index just past ']'
	id    int
}

var promptImageMarkerRe = regexp.MustCompile(`\[image (\d+)\]`)

func imageMarker(id int) string { return fmt.Sprintf("[image %d]", id) }

// AddImage registers an image and inserts its marker at the cursor.
func (p *promptInput) AddImage(data []byte, mediaType, source string) {
	p.nextImageID++
	id := p.nextImageID
	p.attachments = append(p.attachments, promptImage{id: id, data: data, mediaType: mediaType, source: source})
	p.InsertString(imageMarker(id))
}

// liveIDs is the set of attachment ids currently registered.
func (p promptInput) liveIDs() map[int]bool {
	out := make(map[int]bool, len(p.attachments))
	for _, a := range p.attachments {
		out[a.id] = true
	}
	return out
}

// imageSpans returns rune-offset spans for every "[image N]" marker in the text
// whose id is a live attachment, in order of appearance.
func (p promptInput) imageSpans() []imageSpan {
	if len(p.attachments) == 0 {
		return nil
	}
	live := p.liveIDs()
	s := string(p.value)
	var spans []imageSpan
	for _, m := range promptImageMarkerRe.FindAllStringSubmatchIndex(s, -1) {
		id, _ := strconv.Atoi(s[m[2]:m[3]])
		if !live[id] {
			continue
		}
		start := utf8.RuneCountInString(s[:m[0]])
		end := start + utf8.RuneCountInString(s[m[0]:m[1]])
		spans = append(spans, imageSpan{start: start, end: end, id: id})
	}
	return spans
}

// Attachments returns the registered images whose marker still appears in the
// text, in marker order (deduped).
func (p promptInput) Attachments() []promptImage {
	byID := make(map[int]promptImage, len(p.attachments))
	for _, a := range p.attachments {
		byID[a.id] = a
	}
	var out []promptImage
	seen := make(map[int]bool)
	for _, sp := range p.imageSpans() {
		if seen[sp.id] {
			continue
		}
		if a, ok := byID[sp.id]; ok {
			out = append(out, a)
			seen[sp.id] = true
		}
	}
	return out
}

// spanForBackspace returns the chip span to delete when backspace is pressed at
// the current cursor: a span ending at the cursor, or one the cursor sits inside.
func (p promptInput) spanForBackspace() (imageSpan, bool) {
	for _, sp := range p.imageSpans() {
		if p.cursor == sp.end || (p.cursor > sp.start && p.cursor < sp.end) {
			return sp, true
		}
	}
	return imageSpan{}, false
}

// spanForDeleteForward returns the chip span to delete on forward-delete: a span
// starting at the cursor, or one the cursor sits inside.
func (p promptInput) spanForDeleteForward() (imageSpan, bool) {
	for _, sp := range p.imageSpans() {
		if p.cursor == sp.start || (p.cursor > sp.start && p.cursor < sp.end) {
			return sp, true
		}
	}
	return imageSpan{}, false
}

// deleteSpan removes a chip's marker text. The attachment is left in the
// registry (harmless, append-only); Attachments() ignores it once the marker is
// gone, and undo that restores the marker re-includes it.
func (p *promptInput) deleteSpan(sp imageSpan) {
	p.value = append(append([]rune{}, p.value[:sp.start]...), p.value[sp.end:]...)
	p.cursor = sp.start
	p.selectionAnchor = noPromptSelection
}
```

- [ ] **Step 4: Hook atomic delete + registry clear into prompt_input.go**

In `source/clients/cli/internal/ui/prompt_input.go`, add the two fields to `promptInput` (after `canCoalesce bool`, ~line 48):

```go
	attachments []promptImage
	nextImageID int
```

In `deleteBackward` (~line 506), add the span check at the very top (before the selection block):

```go
func (p *promptInput) deleteBackward() {
	if sp, ok := p.spanForBackspace(); ok && !p.selectionRangeOK() {
		p.deleteSpan(sp)
		return
	}
	if start, end, ok := p.selectionRange(); ok {
		// ... existing body unchanged ...
```

In `deleteForward` (~line 520), add at the top similarly:

```go
func (p *promptInput) deleteForward() {
	if sp, ok := p.spanForDeleteForward(); ok && !p.selectionRangeOK() {
		p.deleteSpan(sp)
		return
	}
	if start, end, ok := p.selectionRange(); ok {
		// ... existing body unchanged ...
```

In `SetValue` (~line 111), clear the registry so a fresh prompt starts clean:

```go
func (p *promptInput) SetValue(s string) {
	p.value = []rune(s)
	p.cursor = len(p.value)
	p.selectionAnchor = noPromptSelection
	p.attachments = nil
	p.nextImageID = 0
	p.undo = nil
	p.redo = nil
	p.breakUndoCoalescing()
	p.recalculate()
	p.ensureCursorVisible()
}
```

- [ ] **Step 5: Run to verify it passes + full package**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'AddImage|Chip|Attachments|ResetClears' -count=1
go test ./internal/ui/ -count=1
```
Expected: PASS + package green.

- [ ] **Step 6: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/prompt_image.go source/clients/cli/internal/ui/prompt_image_test.go source/clients/cli/internal/ui/prompt_input.go
git commit -m "feat(cli): prompt image registry + atomic [image N] chip delete"
```

---

### Task 5: prompt widget — chip cursor-skip, selection-swallow, styled render

**Files:**
- Modify: `source/clients/cli/internal/ui/prompt_input.go` (`navigate` left/right default arms ~line 367-384; `selectionRange` ~line 546-556; `renderRowText` ~line 617-632)
- Modify: `source/clients/cli/internal/ui/prompt_image_test.go` (add cases)

**Interfaces:**
- Consumes: `imageSpans()`, `imageSpan` (Task 4).
- Produces: cursor left/right jumps over a chip; a selection that partially covers a chip expands to include the whole chip; chip text renders in the accent style.

- [ ] **Step 1: Write the failing tests**

Append to `source/clients/cli/internal/ui/prompt_image_test.go`:

```go
func TestCursorSkipsChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.InsertString("ab")
	p.AddImage([]byte{1}, "image/png", "") // "ab[image 1]"
	p.CursorStart()
	// move right past 'a','b', then one more right should jump the whole chip.
	p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	before := p.cursor
	p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if p.cursor-before <= 1 {
		t.Fatalf("right arrow should jump over the whole chip, moved %d", p.cursor-before)
	}
	if p.cursor != len([]rune(p.Value())) {
		t.Fatalf("cursor should land at end of chip, got %d of %d", p.cursor, len([]rune(p.Value())))
	}
}

func TestSelectionExpandsToWholeChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "") // "[image 1]"
	p.cursor = 0
	p.selectionAnchor = 3 // anchor inside the chip
	start, end, ok := p.selectionRange()
	if !ok || start != 0 || end != len([]rune(p.Value())) {
		t.Fatalf("selection touching a chip must swallow it whole: start=%d end=%d ok=%v", start, end, ok)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'CursorSkipsChip|SelectionExpands' -count=1
```
Expected: FAIL.

- [ ] **Step 3: Implement cursor-skip**

In `prompt_input.go` `navigate`, change the default (non-cmd/non-alt) left and right arms (~line 372 and ~line 382) to snap across a chip:

```go
		case promptNavLeft:
			switch {
			case cmd:
				next = p.lineStartOffset(old)
			case alt:
				next = p.prevWordOffset(old)
			default:
				next = p.stepLeftOverChip(maxInt(0, old-1))
			}
		case promptNavRight:
			switch {
			case cmd:
				next = p.lineEndOffset(old)
			case alt:
				next = p.nextWordOffset(old)
			default:
				next = p.stepRightOverChip(minInt(len(p.value), old+1))
			}
```

Add the two helpers in `prompt_image.go`:

```go
// stepLeftOverChip snaps an offset that landed inside a chip back to the chip
// start, so a left-arrow treats the chip as a single position.
func (p promptInput) stepLeftOverChip(offset int) int {
	for _, sp := range p.imageSpans() {
		if offset > sp.start && offset < sp.end {
			return sp.start
		}
	}
	return offset
}

// stepRightOverChip snaps an offset inside a chip forward to the chip end.
func (p promptInput) stepRightOverChip(offset int) int {
	for _, sp := range p.imageSpans() {
		if offset > sp.start && offset < sp.end {
			return sp.end
		}
	}
	return offset
}
```

- [ ] **Step 4: Implement selection-swallow**

In `prompt_input.go` `selectionRange` (~line 546), after computing `a, b`, expand to cover any chip the range partially intersects. Replace the function body's tail:

```go
func (p promptInput) selectionRange() (int, int, bool) {
	if p.selectionAnchor < 0 || p.selectionAnchor == p.cursor {
		return 0, 0, false
	}
	a := clampInt(p.selectionAnchor, 0, len(p.value))
	b := clampInt(p.cursor, 0, len(p.value))
	if b < a {
		a, b = b, a
	}
	for _, sp := range p.imageSpans() {
		if a > sp.start && a < sp.end {
			a = sp.start
		}
		if b > sp.start && b < sp.end {
			b = sp.end
		}
	}
	return a, b, a != b
}
```

- [ ] **Step 5: Implement styled chip render**

In `prompt_input.go` `renderRowText` (~line 617), style chip spans with the accent text style. Replace the function with a version that composites text / chip / selection. Add an `accent` style to `promptInputStyles` and route it from the host (see note). Concretely, change `renderRowText` to:

```go
func (p promptInput) renderRowText(row promptRow, selectionStart, selectionEnd int, hasSelection bool) string {
	var b strings.Builder
	for i := row.start; i < row.end; {
		// is i the start of a chip span within this row?
		if sp, ok := p.chipSpanStartingAt(i); ok && sp.end <= row.end {
			seg := string(p.value[sp.start:sp.end])
			if hasSelection && selectionStart <= sp.start && selectionEnd >= sp.end {
				b.WriteString(p.styles.Selection.Render(seg))
			} else {
				b.WriteString(p.styles.Chip.Render(seg))
			}
			i = sp.end
			continue
		}
		// plain rune run until the next chip start or row end
		j := i + 1
		for j < row.end {
			if _, ok := p.chipSpanStartingAt(j); ok {
				break
			}
			j++
		}
		seg := string(p.value[i:j])
		if hasSelection {
			s := maxInt(selectionStart, i)
			e := minInt(selectionEnd, j)
			if s < e {
				b.WriteString(p.styles.Text.Render(string(p.value[i:s])))
				b.WriteString(p.styles.Selection.Render(string(p.value[s:e])))
				b.WriteString(p.styles.Text.Render(string(p.value[e:j])))
				i = j
				continue
			}
		}
		b.WriteString(p.styles.Text.Render(seg))
		i = j
	}
	return b.String()
}

func (p promptInput) chipSpanStartingAt(offset int) (imageSpan, bool) {
	for _, sp := range p.imageSpans() {
		if sp.start == offset {
			return sp, true
		}
	}
	return imageSpan{}, false
}
```

Add `Chip lipgloss.Style` to `promptInputStyles` (~line 18). In the host where `promptInputStyles` is constructed (search `promptInputStyles{` in `model.go`), set `Chip:` to the accent style (e.g. the same style used for accents elsewhere — find the existing `styles.Accent`-equivalent for the prompt). If no obvious accent is wired, set `Chip: styles.Text.Bold(true)` as a minimal distinct treatment.

- [ ] **Step 6: Run tests + full package + build**

```bash
cd source/clients/cli && go test ./internal/ui/ -count=1 && go build -o bin/cercano-cli .
```
Expected: PASS + clean build. (Existing prompt rendering/selection tests must still pass — if any assert exact styled output of plain text, the chip-aware render should produce identical output when there are no chips; fix any that don't.)

- [ ] **Step 7: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/prompt_input.go source/clients/cli/internal/ui/prompt_image.go source/clients/cli/internal/ui/prompt_image_test.go source/clients/cli/internal/ui/model.go
git commit -m "feat(cli): chip cursor-skip, selection-swallow, styled render"
```

---

### Task 6: CLI — image path classification + validation (pure)

**Files:**
- Create: `source/clients/cli/internal/ui/image_paste.go`
- Create: `source/clients/cli/internal/ui/image_paste_test.go`

**Interfaces:**
- Produces:
  - `func parseImagePaths(pasted string) []string` — split pasted text into candidate paths (handles single/double quotes, backslash-escaped spaces, whitespace/newline separation); returns the candidates (not yet validated).
  - `func loadDroppedImage(path string) (data []byte, mediaType string, err error)` — stat + size-check (≤20 MiB) + read + sniff media type; error if not an existing readable image of an accepted type.
  - `func classifyImagePaste(pasted string) (images []droppedImage, ok bool)` where `droppedImage{ data []byte; mediaType, source string }`. `ok` is true only when the whole paste resolves to one or more accepted image files; otherwise the caller treats the paste as literal text.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing tests**

Create `source/clients/cli/internal/ui/image_paste_test.go`:

```go
package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// 1x1 transparent PNG.
var onePxPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestParseImagePathsQuotingAndEscapes(t *testing.T) {
	cases := map[string][]string{
		`/a/b.png`:               {`/a/b.png`},
		`'/a/b c.png'`:           {`/a/b c.png`},
		`"/a/b c.png"`:           {`/a/b c.png`},
		`/a/b\ c.png`:            {`/a/b c.png`},
		"/a/x.png /a/y.png":      {`/a/x.png`, `/a/y.png`},
		"/a/x.png\n/a/y.png":     {`/a/x.png`, `/a/y.png`},
	}
	for in, want := range cases {
		got := parseImagePaths(in)
		if len(got) != len(want) {
			t.Errorf("parseImagePaths(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("parseImagePaths(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestClassifyImagePasteRealFile(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, onePxPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	imgs, ok := classifyImagePaste(img)
	if !ok || len(imgs) != 1 || imgs[0].mediaType != "image/png" || imgs[0].source != img {
		t.Fatalf("expected one png image, got ok=%v imgs=%+v", ok, imgs)
	}
}

func TestClassifyImagePasteNonImageIsLiteral(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	os.WriteFile(txt, []byte("hello"), 0o644)
	if _, ok := classifyImagePaste(txt); ok {
		t.Fatal("a text file path must not classify as an image drop")
	}
	if _, ok := classifyImagePaste("just some pasted prose"); ok {
		t.Fatal("prose must not classify as an image drop")
	}
}

func TestLoadDroppedImageRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.png")
	// header makes it sniff as png; size over the cap.
	buf := append(append([]byte{}, onePxPNG...), make([]byte, (20<<20)+1)...)
	os.WriteFile(big, buf, 0o644)
	if _, _, err := loadDroppedImage(big); err == nil {
		t.Fatal("oversize image must be rejected")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'ParseImagePaths|ClassifyImagePaste|LoadDroppedImage' -count=1
```
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement**

Create `source/clients/cli/internal/ui/image_paste.go`:

```go
package ui

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

const maxDroppedImageBytes = 20 << 20 // 20 MiB, mirrors server llm.maxImageBytes

// acceptedImageTypes maps sniffed/looked-up media types we accept.
var acceptedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type droppedImage struct {
	data      []byte
	mediaType string
	source    string
}

// parseImagePaths splits pasted text into candidate file paths, handling
// single/double-quoted paths, backslash-escaped spaces, and whitespace/newline
// separation between multiple dropped files.
func parseImagePaths(pasted string) []string {
	s := strings.TrimSpace(pasted)
	if s == "" {
		return nil
	}
	var paths []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			paths = append(paths, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i]) // unescape (e.g. "\ " -> " ")
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return paths
}

// loadDroppedImage validates and reads a single image file.
func loadDroppedImage(path string) ([]byte, string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if fi.IsDir() {
		return nil, "", fmt.Errorf("%s is a directory", path)
	}
	if fi.Size() > maxDroppedImageBytes {
		return nil, "", fmt.Errorf("image %s exceeds %d bytes", path, maxDroppedImageBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	mt := sniffImageType(data)
	if !acceptedImageTypes[mt] {
		return nil, "", fmt.Errorf("%s is not an accepted image type", path)
	}
	return data, mt, nil
}

// sniffImageType returns the media type from the content, normalizing the few we
// accept. http.DetectContentType covers png/jpeg/gif/webp.
func sniffImageType(data []byte) string {
	mt := http.DetectContentType(data)
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	return strings.TrimSpace(mt)
}

// classifyImagePaste reports whether the whole pasted text resolves to one or
// more accepted image files. ok=false means the caller should treat the paste as
// literal text.
func classifyImagePaste(pasted string) ([]droppedImage, bool) {
	paths := parseImagePaths(pasted)
	if len(paths) == 0 {
		return nil, false
	}
	var out []droppedImage
	for _, p := range paths {
		data, mt, err := loadDroppedImage(p)
		if err != nil {
			return nil, false // any non-image candidate → whole paste is literal
		}
		out = append(out, droppedImage{data: data, mediaType: mt, source: p})
	}
	return out, true
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'ParseImagePaths|ClassifyImagePaste|LoadDroppedImage' -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/image_paste.go source/clients/cli/internal/ui/image_paste_test.go
git commit -m "feat(cli): image-path paste classification + validation"
```

---

### Task 7: CLI — clipboard image reader (macOS + stub) — SPIKE

**Files:**
- Create: `source/clients/cli/internal/ui/clipboard_image_darwin.go`
- Create: `source/clients/cli/internal/ui/clipboard_image_other.go`
- Create: `source/clients/cli/internal/ui/clipboard_image_test.go`

> **SPIKE NOTE:** Extracting raw image bytes from the macOS pasteboard without a cgo/AppKit dependency is the one unproven step. The implementation below (`pngpaste` if present, else an `osascript` that exports the clipboard PNG to a temp file) is the leading approach — **validate it manually before relying on it** (copy a screenshot, run the helper, confirm bytes come back). If neither path works on the target macOS, report DONE_WITH_CONCERNS and propose the alternative (small cgo NSPasteboard reader) rather than forcing it.

**Interfaces:**
- Produces: `func clipboardImage() (data []byte, mediaType string, ok bool)` — build-tagged per platform. macOS returns PNG bytes + `"image/png"` when the clipboard holds an image; all other platforms return `ok=false`.

- [ ] **Step 1: Write the test (build-tag agnostic; tolerant of "no image in clipboard")**

Create `source/clients/cli/internal/ui/clipboard_image_test.go`:

```go
package ui

import "testing"

// clipboardImage must be safe to call and must never panic. On a machine with no
// image on the clipboard (CI, or non-macOS), it returns ok=false. This test just
// pins the contract; a real macOS smoke check is manual (see the spike note).
func TestClipboardImageContract(t *testing.T) {
	data, mt, ok := clipboardImage()
	if ok {
		if len(data) == 0 || mt == "" {
			t.Fatalf("ok=true must carry data + media type, got len=%d mt=%q", len(data), mt)
		}
	} else {
		if data != nil || mt != "" {
			t.Fatalf("ok=false must return zero values, got len=%d mt=%q", len(data), mt)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd source/clients/cli && go test ./internal/ui/ -run ClipboardImageContract -count=1
```
Expected: FAIL — `clipboardImage` undefined.

- [ ] **Step 3: Implement the non-darwin stub**

Create `source/clients/cli/internal/ui/clipboard_image_other.go`:

```go
//go:build !darwin

package ui

// clipboardImage is unsupported off macOS for now; file drop still works.
func clipboardImage() ([]byte, string, bool) { return nil, "", false }
```

- [ ] **Step 4: Implement the darwin reader**

Create `source/clients/cli/internal/ui/clipboard_image_darwin.go`:

```go
//go:build darwin

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// clipboardImage returns a PNG from the macOS pasteboard if one is present.
// Tries `pngpaste` (Homebrew) first, then falls back to osascript exporting the
// clipboard image to a temp PNG.
func clipboardImage() ([]byte, string, bool) {
	if data, ok := pngpasteClipboard(); ok {
		return data, "image/png", true
	}
	if data, ok := osascriptClipboard(); ok {
		return data, "image/png", true
	}
	return nil, "", false
}

func pngpasteClipboard() ([]byte, bool) {
	bin, err := exec.LookPath("pngpaste")
	if err != nil {
		return nil, false
	}
	out, err := exec.Command(bin, "-").Output() // "-" → write image to stdout
	if err != nil || len(out) == 0 {
		return nil, false
	}
	if sniffImageType(out) != "image/png" {
		return nil, false
	}
	return out, true
}

const clipboardExportScript = `on run
  set outPath to (POSIX path of (path to temporary items)) & "cercano-clipboard.png"
  try
    set theData to (the clipboard as «class PNGf»)
  on error
    return ""
  end try
  set fh to open for access (POSIX file outPath) with write permission
  set eof fh to 0
  write theData to fh
  close access fh
  return outPath
end run`

func osascriptClipboard() ([]byte, bool) {
	scriptPath := filepath.Join(os.TempDir(), "cercano-clip-export.applescript")
	if err := os.WriteFile(scriptPath, []byte(clipboardExportScript), 0o600); err != nil {
		return nil, false
	}
	out, err := exec.Command("osascript", scriptPath).Output()
	if err != nil {
		return nil, false
	}
	pngPath := strings.TrimSpace(string(out))
	if pngPath == "" {
		return nil, false
	}
	data, err := os.ReadFile(pngPath)
	if err != nil || len(data) == 0 || sniffImageType(data) != "image/png" {
		return nil, false
	}
	return data, true
}
```

- [ ] **Step 5: Run the contract test + build; manual smoke (macOS)**

```bash
cd source/clients/cli && go test ./internal/ui/ -run ClipboardImageContract -count=1 && go build ./...
```
Expected: PASS + clean build (on macOS, the darwin file compiles; the test returns ok=false unless an image is on the clipboard).

Manual smoke (macOS, do this — it's the spike validation): copy a screenshot (Cmd+Ctrl+Shift+4), then run a throwaway `package main` that calls into a tiny exported probe, OR temporarily log from the contract test with an image on the clipboard, and confirm `ok=true` with non-empty bytes. Record the result in the report. If it fails, see the spike note.

- [ ] **Step 6: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/clipboard_image_darwin.go source/clients/cli/internal/ui/clipboard_image_other.go source/clients/cli/internal/ui/clipboard_image_test.go
git commit -m "feat(cli): macOS clipboard image reader (+ stub elsewhere)"
```

---

### Task 8: CLI — capture wiring (paste interception + clipboard key)

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (the `tea.PasteMsg` case ~line 622-633; add a `ctrl+v` branch in the `tea.KeyPressMsg` handling)
- Create: `source/clients/cli/internal/ui/image_capture_test.go`

**Interfaces:**
- Consumes: `classifyImagePaste` (Task 6), `clipboardImage` (Task 7), `promptInput.AddImage` (Task 4).
- Produces: dropping image file path(s) replaces the paste with `[image N]` chip(s); a paste whose text is empty/whitespace, and a `ctrl+v` keypress, trigger a clipboard-image check that attaches a chip when an image is present; non-image pastes behave exactly as before.

- [ ] **Step 1: Write the failing test**

The capture entry point should be a pure-ish helper on `Model` that the `PasteMsg` case calls, so it's testable without a TTY. Create `source/clients/cli/internal/ui/image_capture_test.go`:

```go
package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
```

If a `newTestModelWithPrompt()` helper doesn't already exist, add it in this test file:

```go
func newTestModelWithPrompt() *Model {
	m := &Model{}
	m.input = newPromptInput()
	m.input.Focus()
	return m
}
```

(Adjust to however `Model`/`input` are constructed in existing tests — search `model_test.go` for the real constructor and prefer it.)

- [ ] **Step 2: Run to verify it fails**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'HandleImagePaste' -count=1
```
Expected: FAIL — `handleImagePaste` undefined.

- [ ] **Step 3: Implement the capture helpers + wire the message handlers**

Add to `model.go` (near the prompt handling helpers):

```go
// handleImagePaste attaches image chips if the pasted text resolves to image
// file path(s). Returns true if it consumed the paste (caller must NOT insert the
// text literally); false means treat the paste as normal text.
func (m *Model) handleImagePaste(pasted string) bool {
	imgs, ok := classifyImagePaste(pasted)
	if !ok {
		return false
	}
	for _, img := range imgs {
		m.input.AddImage(img.data, img.mediaType, img.source)
	}
	return true
}

// handleClipboardImage attaches a chip if the OS clipboard holds an image.
// Returns true if it attached one.
func (m *Model) handleClipboardImage() bool {
	data, mt, ok := clipboardImage()
	if !ok {
		return false
	}
	m.input.AddImage(data, mt, "")
	return true
}
```

Update the `tea.PasteMsg` case in `Update` (~line 622) so it routes through capture first:

```go
	case tea.PasteMsg:
		if m.contentPageActive() || m.pendingConfirm != nil {
			return m, nil
		}
		m = m.preparePromptInput()
		// A drag-dropped image arrives as a paste of its path; a copied image may
		// arrive as an empty/whitespace paste (bytes live on the clipboard).
		if strings.TrimSpace(msg.Content) == "" {
			if m.handleClipboardImage() {
				m.relayout()
				return m, nil
			}
		} else if m.handleImagePaste(msg.Content) {
			m.relayout()
			return m, nil
		}
		var cmd tea.Cmd
		prevVal := m.input.Value()
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != prevVal {
			m.relayout()
		}
		return m, cmd
```

Add a `ctrl+v` fallback in the key handling (find where other ctrl-keys are matched in the `tea.KeyPressMsg` path of `Update`; add before the prompt receives the key). The exact integration point is the `tea.KeyPressMsg` case (~line 635). Add, after the pending-confirm gate and before forwarding to the prompt:

```go
		if msg.String() == "ctrl+v" && !m.contentPageActive() && m.pendingConfirm == nil {
			m = m.preparePromptInput()
			if m.handleClipboardImage() {
				m.relayout()
				return m, nil
			}
			// no image on clipboard → fall through to normal handling
		}
```

(Note: `m` is a value receiver in this codebase's `Update`; the helpers use pointer receivers, so call them on `&m` if needed — match the surrounding code's `m = m.preparePromptInput()` value-style by making `handleImagePaste`/`handleClipboardImage` pointer-receiver methods invoked on `&m`. Verify against how `m.input.Update` is called and keep the value/pointer usage consistent so the prompt mutation persists.)

- [ ] **Step 4: Run tests + full package + build**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'HandleImagePaste' -count=1
go test ./internal/ui/ -count=1 && go build -o bin/cercano-cli .
```
Expected: PASS + green + clean build.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/image_capture_test.go
git commit -m "feat(cli): capture image drops + clipboard paste into prompt chips"
```

---

### Task 9: CLI — send images with the turn + history display

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (the Enter handler ~line 759-775; `submit` ~line 1084-1106)
- Modify: `source/clients/cli/internal/ui/main_agent_driver.go` (`Submit` ~line 35-51)
- Modify: `source/clients/cli/internal/ui/main_agent_driver_test.go` (if it asserts `Submit`'s signature) 

**Interfaces:**
- Consumes: `promptInput.Attachments()` (Task 4), `agentclient.InlineImage` + variadic `StreamChat` (Task 3).
- Produces: `mainAgentDriver.Submit(ctx, input string, images []agentclient.InlineImage)`; `Model.submit(text string, images []agentclient.InlineImage)`; sent user entry shows the marker text + an "(N images)" suffix when images are attached.

- [ ] **Step 1: Write the failing test**

Add to `source/clients/cli/internal/ui/image_capture_test.go`:

```go
import "cercano/source/server/pkg/agentclient" // add to existing imports

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
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'PromptAttachmentsMapToInlineImages' -count=1
```
Expected: FAIL — `promptImagesToInline` undefined.

- [ ] **Step 3: Implement the mapper + thread images through submit/driver**

Add to `model.go`:

```go
// promptImagesToInline converts prompt attachments to agentclient images.
func promptImagesToInline(atts []promptImage) []agentclient.InlineImage {
	if len(atts) == 0 {
		return nil
	}
	out := make([]agentclient.InlineImage, 0, len(atts))
	for _, a := range atts {
		out = append(out, agentclient.InlineImage{
			Index:     int32(a.id),
			Data:      a.data,
			MediaType: a.mediaType,
		})
	}
	return out
}
```

(Ensure `model.go` imports `cercano/source/server/pkg/agentclient` — it already does for the agent client.)

In the Enter handler (~line 759-775), capture attachments BEFORE clearing the input, and pass them to submit:

```go
		text := strings.TrimSpace(m.input.Value())
		images := promptImagesToInline(m.input.Attachments())
		m.input.SetValue("")
		// ... existing reset/relayout ...
		return m.submit(text, images)
```

(Match the existing control flow exactly — keep whatever it currently does between reading text and calling submit; only add the `images` capture before `SetValue("")` and pass it through.)

Change `submit` (~line 1084):

```go
func (m Model) submit(text string, images []agentclient.InlineImage) (tea.Model, tea.Cmd) {
	// ... unchanged history/slash handling ...
	// User turn — show markers + image count
	content := text
	if len(images) > 0 {
		content = strings.TrimSpace(content)
		content += fmt.Sprintf("  (%d image%s)", len(images), plural(len(images)))
	}
	m.chat.AppendEntry(&Entry{Role: RoleUser, Content: content})
	// ... assistant placeholder, refreshViewport ...
	wd, _ := os.Getwd()
	driver := &mainAgentDriver{agent: m.agent, convID: m.convID, workDir: wd}
	cmd, cancel, err := driver.Submit(context.Background(), text, images)
	// ... unchanged error handling + cancel storage ...
}
```

Add a tiny `plural` helper if none exists:

```go
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
```

(If `fmt` isn't imported in `model.go`, add it. If a pluralization helper already exists, use it.)

Update `mainAgentDriver.Submit` (~line 35):

```go
func (d *mainAgentDriver) Submit(ctx context.Context, input string, images []agentclient.InlineImage) (tea.Cmd, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)
	ch, err := d.agent.StreamChat(ctx, d.convID, input, d.workDir, images...)
	// ... unchanged ...
}
```

- [ ] **Step 4: Update any other submit/Submit callers**

Search for callers so the build stays green:

```bash
cd source/clients/cli && grep -rn "\.submit(\|driver.Submit(\|\.Submit(context" internal/ui/ | grep -v _test
```
Update each to pass the new `images` argument (`nil` where there are no images, e.g. slash-command or history re-submit paths if any call `submit`).

- [ ] **Step 5: Run tests + full module + build**

```bash
cd source/clients/cli && go test ./... -count=1 && go build -o bin/cercano-cli .
```
Expected: green + clean build.

- [ ] **Step 6: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/main_agent_driver.go source/clients/cli/internal/ui/main_agent_driver_test.go source/clients/cli/internal/ui/image_capture_test.go
git commit -m "feat(cli): send attached images with the turn; show image count in history"
```

---

### Task 10: CLI — vision-capability warning

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (add a cached `supportsVision` flag + refresh; render a notice when an image is attached and vision is unsupported)
- Create: `source/clients/cli/internal/ui/vision_notice_test.go`

**Interfaces:**
- Consumes: `agentclient.Client.GetProviderCapabilities(ctx) (ProviderCaps, error)` with `ProviderCaps.SupportsVision` (`pkg/agentclient/client.go:1051`); `promptInput.Attachments()`.
- Produces: `Model.visionNotice() string` — returns a dim warning string when `len(m.input.Attachments()) > 0 && !m.supportsVision`, else `""`. The notice is rendered near the prompt (folded into the existing footer/status render).

- [ ] **Step 1: Write the failing test**

Create `source/clients/cli/internal/ui/vision_notice_test.go`:

```go
package ui

import (
	"strings"
	"testing"
)

func TestVisionNoticeShownWhenUnsupportedAndImageAttached(t *testing.T) {
	m := newTestModelWithPrompt()
	m.supportsVision = false
	m.input.AddImage([]byte{1}, "image/png", "")
	if m.visionNotice() == "" {
		t.Fatal("expected a vision notice when an image is attached and vision is unsupported")
	}
}

func TestVisionNoticeHiddenWhenSupported(t *testing.T) {
	m := newTestModelWithPrompt()
	m.supportsVision = true
	m.input.AddImage([]byte{1}, "image/png", "")
	if m.visionNotice() != "" {
		t.Fatal("no notice when the model supports vision")
	}
}

func TestVisionNoticeHiddenWhenNoImage(t *testing.T) {
	m := newTestModelWithPrompt()
	m.supportsVision = false
	if n := m.visionNotice(); n != "" {
		t.Fatalf("no notice without an attached image, got %q", n)
	}
	_ = strings.TrimSpace
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'VisionNotice' -count=1
```
Expected: FAIL — `supportsVision` field / `visionNotice` undefined.

- [ ] **Step 3: Implement**

Add a `supportsVision bool` field to the `Model` struct (find the struct in `model.go`). Add:

```go
// visionNotice returns a dim warning when images are attached but the active
// model can't accept them. Interim UX until capability-aware routing lands.
func (m Model) visionNotice() string {
	if len(m.input.Attachments()) == 0 || m.supportsVision {
		return ""
	}
	return m.styles.Muted.Render("⚠ active model can't see images")
}
```

(Use the codebase's existing muted/dim style — search `styles.Muted` or the prompt-area dim style and match it. If the `Model` styles field differs, adapt.)

Refresh `supportsVision` where capabilities are already (or should be) fetched: at startup and after a model/profile/locus change. Find where the CLI calls `GetProviderCapabilities` (search `GetProviderCapabilities` in `internal/ui`); if it's already fetched for another purpose, set `m.supportsVision = caps.SupportsVision` there. If not fetched anywhere, add a command at startup:

```go
func fetchVisionCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		caps, err := ag.GetProviderCapabilities(ctx)
		if err != nil {
			return nil
		}
		return visionCapsMsg{supported: caps.SupportsVision}
	}
}

type visionCapsMsg struct{ supported bool }
```

Handle `visionCapsMsg` in `Update` by setting `m.supportsVision = msg.supported`, and issue `fetchVisionCmd(m.agent)` from `Init` (or wherever startup commands are batched) and again after a `permissionModeChangedMsg`/`settingsThemeMsg`-style model/profile change if one exists. Render `visionNotice()` in the footer/status area near the prompt (find the prompt footer render and append the notice on its own line when non-empty).

- [ ] **Step 4: Run tests + full module + build**

```bash
cd source/clients/cli && go test ./... -count=1 && go build -o bin/cercano-cli .
```
Expected: green + clean build.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/vision_notice_test.go
git commit -m "feat(cli): warn when active model can't see attached images"
```

---

### Task 11: Docs

**Files:**
- Modify: `docs/agent/llm-backend-notes.md` (note user image input now reaches the model)
- Modify: `source/clients/cli/README.md` if present, else `docs/features/cli/README.md` (document drop/paste image usage)

**Interfaces:** none (docs only).

- [ ] **Step 1: Document the feature**

In `docs/agent/llm-backend-notes.md`, after the conformance matrix section, add:

```markdown
### User image input

`cercano-cli` can attach images to a turn: drag an image file onto the terminal
(it arrives as a path paste) or paste a copied image (Cmd+V / Ctrl+V on macOS).
Each shows as an atomic `[image N]` chip; backspace deletes the whole chip. The
image travels as bytes in `ProcessRequestRequest.images` and the server splices it
into the user message at its marker (`buildUserBlocks`), so the model sees the
image where you dropped it. PNG/JPEG/GIF/WEBP, ≤20 MiB. If the active model can't
see images the CLI warns but still sends (capability-aware routing will supersede
this).
```

Locate the CLI usage doc (`ls source/clients/cli/README.md docs/features/cli/README.md`) and add a short "Attaching images" subsection mirroring the above in user-facing terms (drag a file or Cmd+V; backspace removes a chip).

- [ ] **Step 2: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add docs/agent/llm-backend-notes.md docs/features/cli/README.md source/clients/cli/README.md 2>/dev/null
git commit -m "docs: document CLI image drop/paste input"
```

---

## Self-Review

**1. Spec coverage:**
- §1a file drop → Tasks 6 (classify) + 8 (wiring). ✅
- §1b clipboard image → Tasks 7 (reader/spike) + 8 (trigger). ✅
- §2 chip atomicity / registry / Attachments → Task 4; cursor-skip/selection/render → Task 5. ✅
- §3 proto + buildUserBlocks + agentclient + send → Tasks 1, 2, 3, 9. ✅
- §4 vision warn-but-allow → Task 10. ✅
- §5 limits (20 MiB, types, literal fallthrough) → Task 6. ✅
- §6 components → all tasks map to the named files. ✅
- §7 testing → each task carries the spec's test cases. ✅
- §8 out-of-scope honored (no routing, no non-image files, macOS-first clipboard, no thumbnails, no URL/SSRF work).

**2. Placeholder scan:** No TBD/TODO. The clipboard reader is a flagged SPIKE with a concrete implementation + manual validation step (not a placeholder). A few steps say "match the surrounding code's value/pointer style" / "find the existing muted style" — these are integration-fit instructions against real, named code, with the exact behavior specified; acceptable.

**3. Type consistency:** `[image N]` marker + `\[image (\d+)\]` regex identical on both ends. `InlineImage` exists at three layers with consistent fields: `proto.InlineImage{Index int32, Data []byte, MediaType string}`, `agentclient.InlineImage{Index int32,...}`, `agent.InlineImage{Index int,...}` (server maps int32→int in `mapInlineImages`). `promptImage{id,data,mediaType,source}` → `agentclient.InlineImage` via `promptImagesToInline` (id→Index int32). `buildUserBlocks(string, []agent.InlineImage)` signature consistent across toolloop + llm_adapter. `StreamChat(..., images ...InlineImage)` variadic consumed by `Submit(..., images []agentclient.InlineImage)` via `images...`. `clipboardImage() ([]byte,string,bool)` and `classifyImagePaste(string) ([]droppedImage,bool)` consistent with their callers in Task 8.
