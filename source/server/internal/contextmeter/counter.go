package contextmeter

import (
	"sync"
)

// Counter tracks the running token usage for a single conversation. Safe for
// concurrent calls from the agent's turn flow (which may interleave Add and
// Used / Percent reads from different goroutines).
type Counter struct {
	mu     sync.Mutex
	tok    Tokenizer
	max    int
	tokens int
}

// NewCounter constructs a counter against the given tokenizer and model max.
// max <= 0 falls back to a sensible default of 128 000.
func NewCounter(tok Tokenizer, max int) *Counter {
	if max <= 0 {
		max = 128_000
	}
	if tok == nil {
		tok = Default()
	}
	return &Counter{tok: tok, max: max}
}

// Add counts s and adds the result to the running total.
func (c *Counter) Add(s string) {
	if c == nil || s == "" {
		return
	}
	n := c.tok.Count(s)
	c.mu.Lock()
	c.tokens += n
	c.mu.Unlock()
}

// AddCount adds a literal token count (when the upstream provider already
// reported it, e.g. Ollama's prompt_eval_count / eval_count).
func (c *Counter) AddCount(n int) {
	if c == nil || n <= 0 {
		return
	}
	c.mu.Lock()
	c.tokens += n
	c.mu.Unlock()
}

// Reset clears the running total.
func (c *Counter) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.tokens = 0
	c.mu.Unlock()
}

// Used returns the current running token count.
func (c *Counter) Used() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens
}

// Max returns the model's conventional context-window size.
func (c *Counter) Max() int {
	if c == nil {
		return 0
	}
	return c.max
}

// SetMax updates the model max (when the agent's active model changes
// mid-conversation via /config or /model).
func (c *Counter) SetMax(max int) {
	if c == nil || max <= 0 {
		return
	}
	c.mu.Lock()
	c.max = max
	c.mu.Unlock()
}

// Percent returns Used/Max as a 0..1 float (capped at 1.0).
func (c *Counter) Percent() float64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.max <= 0 {
		return 0
	}
	p := float64(c.tokens) / float64(c.max)
	if p > 1 {
		return 1
	}
	return p
}

// Registry maps conversation ids → counters. Safe for concurrent access.
// Created on first use per conversation id; the agent's storeConversationTurn
// is the natural place to populate it.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{counters: map[string]*Counter{}}
}

// Get returns the counter for id, creating one if none exists. tokModel is
// only consulted on creation.
func (r *Registry) Get(id, tokModel string) *Counter {
	if r == nil || id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[id]
	if !ok {
		c = NewCounter(Default(), ModelMax(tokModel))
		r.counters[id] = c
	}
	return c
}

// Drop removes a counter (e.g. on /clear).
func (r *Registry) Drop(id string) {
	if r == nil || id == "" {
		return
	}
	r.mu.Lock()
	delete(r.counters, id)
	r.mu.Unlock()
}
