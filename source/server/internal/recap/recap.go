// Package recap maintains a short, living, locally-generated summary of the
// work in a conversation. Generation is debounced per-conversation and runs
// off the request path; failures are swallowed so a turn is never blocked.
package recap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/conversation"
)

// CompleteFunc runs a single local-model completion. The wiring layer adapts
// the local provider into this; recap never sees a cloud path.
type CompleteFunc func(ctx context.Context, prompt string) (string, error)

// Store is the subset of conversation.Store the generator needs.
type Store interface {
	Get(ctx context.Context, conversationID string) (conversation.Info, error)
	GetTurns(ctx context.Context, conversationID string) ([]conversation.Turn, error)
	UpdateRecap(ctx context.Context, conversationID, recap string) error
	SetGeneratedTitle(ctx context.Context, conversationID, title string) error
}

const (
	maxRecapChars = 160
	maxTitleChars = 48
	genTimeout    = 30 * time.Second
)

// Generator debounces recap regeneration per conversation.
type Generator struct {
	store    Store
	complete CompleteFunc
	debounce time.Duration
	maxTurns int

	mu     sync.Mutex
	timers map[string]*time.Timer
}

// New builds a Generator. debounce is the minimum quiet period after the last
// Schedule before generation fires; maxTurns caps how many recent turns feed
// the prompt.
func New(store Store, complete CompleteFunc, debounce time.Duration, maxTurns int) *Generator {
	return &Generator{
		store:    store,
		complete: complete,
		debounce: debounce,
		maxTurns: maxTurns,
		timers:   make(map[string]*time.Timer),
	}
}

// Schedule requests a (debounced) recap regeneration for the conversation.
// Rapid calls coalesce into a single generation after the quiet period.
func (g *Generator) Schedule(conversationID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.timers[conversationID]; ok {
		t.Reset(g.debounce)
		return
	}
	g.timers[conversationID] = time.AfterFunc(g.debounce, func() {
		g.mu.Lock()
		delete(g.timers, conversationID)
		g.mu.Unlock()
		g.regenerate(conversationID)
	})
}

func (g *Generator) regenerate(conversationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), genTimeout)
	defer cancel()

	info, err := g.store.Get(ctx, conversationID)
	if err != nil {
		return // conversation gone; nothing to do
	}
	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil || len(turns) == 0 {
		return
	}
	prompt := buildPrompt(info.Recap, turns, g.maxTurns)

	out, err := g.complete(ctx, prompt)
	if err != nil {
		return // keep prior recap on failure
	}
	line := firstLine(out, maxRecapChars)
	if line == "" {
		return
	}
	_ = g.store.UpdateRecap(ctx, conversationID, line)

	// Derive a short session title from the fresh recap (next-tightest rung).
	// Best-effort: failures leave the existing title untouched.
	titleOut, err := g.complete(ctx, buildTitlePrompt(line))
	if err != nil {
		return
	}
	title := firstLine(titleOut, maxTitleChars)
	if title == "" {
		return
	}
	_ = g.store.SetGeneratedTitle(ctx, conversationID, title)
}

// buildPrompt produces an incremental summarization prompt from the prior
// recap plus the most recent maxTurns turns.
func buildPrompt(prior string, turns []conversation.Turn, maxTurns int) string {
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	var b strings.Builder
	for _, t := range turns {
		c := t.Content
		if len(c) > 500 {
			c = c[:500]
		}
		fmt.Fprintf(&b, "%s: %s\n", t.Role, c)
	}
	priorLine := prior
	if priorLine == "" {
		priorLine = "(none yet)"
	}
	return fmt.Sprintf(
		"You maintain a one-line running summary of a coding session.\n"+
			"Prior summary: %s\n\nRecent activity:\n%s\n"+
			"Write ONE updated line, past tense, at most %d characters, describing "+
			"what the session has accomplished so far. Output ONLY the line, no quotes, no preamble.",
		priorLine, b.String(), maxRecapChars)
}

// buildTitlePrompt turns a one-line recap into a short session title.
func buildTitlePrompt(recap string) string {
	return fmt.Sprintf(
		"Given this one-line summary of a coding session, write a short title "+
			"of 3 to 6 words in Title Case, with no quotes and no trailing "+
			"punctuation.\nSummary: %s\nOutput ONLY the title.",
		recap)
}

// firstLine returns the first non-empty trimmed line, truncated to maxChars
// (with an ellipsis when cut).
func firstLine(s string, maxChars int) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		r := []rune(ln)
		if len(r) > maxChars {
			return string(r[:maxChars-1]) + "…"
		}
		return ln
	}
	return ""
}
