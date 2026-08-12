// Package visioninspect implements the capabilities.VisionService that backs
// the inspect_image tool. It turns a stored image attachment plus a focused
// question into a single, tool-less inference call against the configured
// vision-tier model, and returns the answer in a stable envelope.
//
// The reasoning model never sees raw image bytes (it gets placeholders — see
// internal/agent.RewriteImagesToPlaceholders and internal/visionattach). This
// package is the one place an image and a question meet a model that can
// actually see pixels. It exposes NO tools to the vision model: the call is a
// leaf, not another agentic loop.
//
// It is wired into capabilities.Services.Vision by the server, which supplies
// the per-conversation attachment store and a resolver that yields the current
// vision provider + model for the active locus/runtime. This package imports
// visionattach + inference + llm but NOT capabilities, so capabilities stays
// free of an inference/visionattach dependency (the interface lives there; the
// implementation lives here).
package visioninspect

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/visionattach"
)

// errNoInner is returned when a wrapper has no underlying vision service.
var errNoInner = errors.New("vision is not configured")

// DefaultTimeout bounds a single vision call. Vision models on local runtimes
// can be slow to first token, but a single focused question should not run
// unbounded — a hung vision call must not wedge the reasoning turn.
const DefaultTimeout = 90 * time.Second

// defaultMaxTokens caps the vision model's answer. Inspection answers are
// short by design (a focused question, not an essay), and a cap keeps a
// runaway generation from stalling the turn.
const defaultMaxTokens = 512

// systemPrompt steers the vision model toward a direct, grounded answer and
// away from tool-use chatter or hedging. The model is given no tools, so this
// is purely about answer shape.
const systemPrompt = "You are a vision assistant. Answer the user's question about the provided image directly and concretely, describing only what is actually visible. If the image does not contain the information asked for, say so plainly. Do not ask follow-up questions."

// Resolved names the vision provider and model to use for one inspection.
type Resolved struct {
	Provider inference.Provider // the backend that will answer
	Model    string             // the concrete vision model id (may be "")
}

// Resolver yields the current vision target for the active locus/runtime, and
// ok=false when no vision model is configured/reachable. The server supplies
// this so this package stays out of config/provider-selection internals. It is
// called on every Available()/Inspect() so a config change takes effect without
// rebuilding the inspector.
type Resolver func() (Resolved, bool)

// Inspector implements capabilities.VisionService over an attachment store and
// a vision-target resolver.
type Inspector struct {
	store    *visionattach.Store
	resolve  Resolver
	timeout  time.Duration
	maxToks  int
	sysPromt string
}

// Ensure the interface is satisfied at compile time.
var _ capabilities.VisionService = (*Inspector)(nil)

// New builds an Inspector. store and resolve are required; a nil store or nil
// resolver yields an inspector that reports vision unavailable (never panics),
// so a partially-wired server degrades gracefully.
func New(store *visionattach.Store, resolve Resolver) *Inspector {
	return &Inspector{
		store:    store,
		resolve:  resolve,
		timeout:  DefaultTimeout,
		maxToks:  defaultMaxTokens,
		sysPromt: systemPrompt,
	}
}

// WithTimeout overrides the per-call timeout (non-positive keeps the default).
func (in *Inspector) WithTimeout(d time.Duration) *Inspector {
	if d > 0 {
		in.timeout = d
	}
	return in
}

// Available reports whether a vision model is configured and reachable. A nil
// store or resolver, or a resolver miss, means unavailable.
func (in *Inspector) Available() bool {
	if in == nil || in.store == nil || in.resolve == nil {
		return false
	}
	r, ok := in.resolve()
	return ok && r.Provider != nil && r.Model != ""
}

// Lookup reports whether an image with imageID is currently held for convID.
func (in *Inspector) Lookup(convID, imageID string) bool {
	if in == nil || in.store == nil {
		return false
	}
	_, ok := in.store.Lookup(convID, imageID)
	return ok
}

// Inspect asks the vision model question about the image and returns the answer
// envelope. It is only reached after inspect_image has confirmed Available and
// Lookup, but it re-checks both defensively.
func (in *Inspector) Inspect(ctx context.Context, convID, imageID, question string) (capabilities.VisionAnswer, error) {
	if in == nil || in.store == nil || in.resolve == nil {
		return capabilities.VisionAnswer{}, errors.New("vision is not configured")
	}
	att, ok := in.store.Lookup(convID, imageID)
	if !ok {
		// inspect_image gates on Lookup first, so this is a defensive path
		// (e.g. the attachment was cleared between the tool's check and here).
		return capabilities.VisionAnswer{}, fmt.Errorf("image %s is no longer available", imageID)
	}
	r, ok := in.resolve()
	if !ok || r.Provider == nil || r.Model == "" {
		return capabilities.VisionAnswer{}, errors.New("no vision model is configured")
	}

	req := inference.Call{
		Model:     r.Model,
		System:    in.sysPromt,
		MaxTokens: in.maxToks,
		// No Tools: the vision model gets a single question, not an agentic loop.
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Blocks: []llm.Block{
				{Type: llm.BlockText, Text: strings.TrimSpace(question)},
				{Type: llm.BlockImage, MediaType: att.MediaType, ImageData: base64.StdEncoding.EncodeToString(att.Data)},
			},
		}},
	}

	callCtx := ctx
	if in.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, in.timeout)
		defer cancel()
	}

	res, err := r.Provider.Chat(callCtx, req)
	if err != nil {
		return capabilities.VisionAnswer{}, err
	}

	answer := firstText(res.Blocks)
	if strings.TrimSpace(answer) == "" {
		return capabilities.VisionAnswer{}, errors.New("vision model returned no answer")
	}

	source := r.Provider.Name()
	if source != "" && r.Model != "" {
		source = source + ":" + r.Model
	} else if r.Model != "" {
		source = r.Model
	}
	return capabilities.VisionAnswer{
		Answer: strings.TrimSpace(answer),
		Source: source,
	}, nil
}

// firstText concatenates the text blocks of a response, ignoring reasoning and
// any stray non-text blocks. A vision model with no tools should return plain
// text; joining is defensive against providers that split an answer across
// multiple text blocks.
func firstText(blocks []llm.Block) string {
	var b strings.Builder
	for _, bl := range blocks {
		if bl.Type == llm.BlockText && bl.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(bl.Text)
		}
	}
	return b.String()
}
