package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
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

type captureEveryProvider struct {
	scripts  [][]llm.Block
	capture  [][]llm.Message
	textOnly bool
}

func (p *captureEveryProvider) Name() string { return "capture-every" }
func (p *captureEveryProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true, SupportsVision: !p.textOnly}
}
func (p *captureEveryProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *captureEveryProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.capture = append(p.capture, req.Messages)
	idx := len(p.capture) - 1
	if idx >= len(p.scripts) {
		idx = len(p.scripts) - 1
	}
	return &scriptedStream{events: blocksToEvents(p.scripts[idx])}, nil
}

type imageTool struct {
	data       string
	permission agenttools.Permission
}

func (imageTool) Name() string        { return "screenshot" }
func (imageTool) Description() string { return "returns an image" }
func (t imageTool) Permission() agenttools.Permission {
	if t.permission != "" {
		return t.permission
	}
	return agenttools.PermR
}
func (imageTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t imageTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	return &agenttools.Result{
		Type: agenttools.ResultText,
		Text: "screenshot captured",
		Images: []llm.Block{{
			Type:      llm.BlockImage,
			MediaType: "image/png",
			ImageData: t.data,
		}},
	}, nil
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

func TestRunToolLoop_RewritesHistoricalImagesWhenVisionStoreSet(t *testing.T) {
	store := visionattach.NewStore()
	prov := &capturingProvider{}
	largeEncodedImage := b64(strings.Repeat("OLDPNG", 4096))

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:  prov,
		Registry:  emptyRegistry(t),
		UserInput: "continue",
		ConvHistory: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "previous screenshot:"},
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: largeEncodedImage},
		}}},
		ConversationID: "conv-history",
		VisionStore:    store,
		MaxIterations:  1,
	})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if len(prov.captured) < 2 {
		t.Fatalf("captured messages = %d, want history plus current user", len(prov.captured))
	}
	history := prov.captured[0]
	for _, b := range history.Blocks {
		if b.Type == llm.BlockImage {
			t.Fatalf("raw historical image block reached provider-facing request: %+v", history.Blocks)
		}
	}
	if !messageContainsText(history, "inspect_image") {
		t.Fatalf("historical image was not rewritten to inspect_image placeholder: %+v", history.Blocks)
	}
	if messageContainsText(history, largeEncodedImage) {
		t.Fatal("large historical base64 payload leaked into provider-facing text")
	}
	if got := store.Count("conv-history"); got != 1 {
		t.Fatalf("store.Count = %d, want historical image registered", got)
	}
}

func TestRunToolLoop_RewritesToolResultImagesBeforeNextProviderCall(t *testing.T) {
	for _, permission := range []agenttools.Permission{agenttools.PermR, agenttools.PermW} {
		for _, tc := range []struct {
			name                     string
			textOnly, withStore      bool
			convID                   string
			wantPlaceholder, wantRaw bool
		}{
			{"vision-store", false, true, "conv-tool-image", true, false},
			{"text-only-store", true, true, "conv-tool-image", true, false},
			{"vision-no-store", false, false, "conv-tool-image", false, true},
			{"text-only-no-store", true, false, "conv-tool-image", false, false},
			{"text-only-no-conversation", true, true, "", false, false},
		} {
			t.Run(string(permission)+"/"+tc.name, func(t *testing.T) {
				store := visionattach.NewStore()
				largeEncodedImage := b64(strings.Repeat("TOOLPNG", 4096))
				prov := &captureEveryProvider{textOnly: tc.textOnly, scripts: [][]llm.Block{
					{{Type: llm.BlockToolUse, ToolUseID: "call_1", ToolName: "screenshot", ToolInput: []byte(`{}`)}},
					{{Type: llm.BlockText, Text: "done"}},
				}}
				reg := emptyRegistry(t)
				reg.MustRegister(imageTool{data: largeEncodedImage, permission: permission})
				in := ToolLoopInput{
					Provider: prov, Registry: reg, UserInput: "take a screenshot",
					ConversationID: tc.convID, MaxIterations: 2,
					PermissionRequester: func(context.Context, string, string, json.RawMessage, llm.Permission, bool) (bool, error) {
						return true, nil
					},
				}
				if tc.withStore {
					in.VisionStore = store
				}
				_, err := RunToolLoop(t.Context(), in)
				if err != nil {
					t.Fatalf("RunToolLoop: %v", err)
				}
				if len(prov.capture) != 2 {
					t.Fatalf("provider calls = %d, want 2", len(prov.capture))
				}
				var placeholder string
				raw, omitted, result := false, false, false
				for _, m := range prov.capture[1] {
					for _, b := range m.Blocks {
						if b.Type == llm.BlockImage {
							raw = true
						}
						if b.Type == llm.BlockText && strings.Contains(b.Text, "inspect_image") {
							placeholder = b.Text
						}
						if b.Type == llm.BlockToolResult && b.ToolUseRef == "call_1" {
							result = strings.Contains(b.Content, "screenshot captured")
							omitted = strings.Contains(b.Content, "no vision support")
						}
					}
					if messageContainsText(m, largeEncodedImage) {
						t.Fatal("base64 leaked into text")
					}
				}
				if !result {
					t.Fatal("tool result missing")
				}
				if raw != tc.wantRaw {
					t.Fatalf("raw image = %v, want %v", raw, tc.wantRaw)
				}
				if (placeholder != "") != tc.wantPlaceholder {
					t.Fatalf("unexpected placeholder: %q", placeholder)
				}
				if omitted != (!tc.wantPlaceholder && !tc.wantRaw) {
					t.Fatalf("unexpected omission: %v", omitted)
				}
				if tc.wantPlaceholder {
					fields := strings.Fields(placeholder)
					if len(fields) < 2 {
						t.Fatalf("invalid placeholder: %q", placeholder)
					}
					att, ok := store.Lookup(tc.convID, fields[1])
					if !ok || string(att.Data) != strings.Repeat("TOOLPNG", 4096) || att.MediaType != "image/png" {
						t.Fatal("placeholder does not resolve to original PNG")
					}
				} else if store.Count(tc.convID) != 0 {
					t.Fatal("unexpected stored image")
				}
			})
		}
	}
}

func messageContainsText(m llm.Message, sub string) bool {
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText && strings.Contains(b.Text, sub) {
			return true
		}
	}
	return false
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
