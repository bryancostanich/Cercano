package visioninspect

import (
	"context"
	"strings"
	"sync"

	"cercano/source/server/internal/capabilities"
)

// CachingInspector wraps a capabilities.VisionService with a per-conversation,
// in-memory cache keyed by (image_id, normalized question). It exists so a
// reasoning model that asks the same visual question twice in a conversation
// pays the vision-model cost once.
//
// Scope and lifetime match the attachment store: non-persistent (V1). The cache
// holds ONLY successful answers — unavailable/stale/error results are never
// cached, so a transient failure or a since-reattached image is always retried.
// Available and Lookup pass straight through to the wrapped service, so cache
// state never masks a real availability or presence check.
type CachingInspector struct {
	inner capabilities.VisionService

	mu     sync.Mutex
	byConv map[string]map[string]capabilities.VisionAnswer // convID -> cacheKey -> answer
}

var _ capabilities.VisionService = (*CachingInspector)(nil)

// NewCaching wraps inner with a per-conversation answer cache. A nil inner
// yields a cache that reports unavailable and never answers (defensive).
func NewCaching(inner capabilities.VisionService) *CachingInspector {
	return &CachingInspector{
		inner:  inner,
		byConv: make(map[string]map[string]capabilities.VisionAnswer),
	}
}

// Available passes through — the cache never claims availability the wrapped
// service does not have.
func (c *CachingInspector) Available() bool {
	if c == nil || c.inner == nil {
		return false
	}
	return c.inner.Available()
}

// Lookup passes through — presence is a live property of the attachment store,
// not something the answer cache should shadow.
func (c *CachingInspector) Lookup(convID, imageID string) bool {
	if c == nil || c.inner == nil {
		return false
	}
	return c.inner.Lookup(convID, imageID)
}

// Inspect returns a cached answer for a repeated (image_id, normalized question)
// within the conversation, otherwise delegates and caches a successful result.
// Errors are returned as-is and never cached.
func (c *CachingInspector) Inspect(ctx context.Context, convID, imageID, question string) (capabilities.VisionAnswer, error) {
	if c == nil || c.inner == nil {
		return capabilities.VisionAnswer{}, errNoInner
	}
	key := cacheKey(imageID, question)

	if ans, ok := c.get(convID, key); ok {
		return ans, nil
	}

	ans, err := c.inner.Inspect(ctx, convID, imageID, question)
	if err != nil {
		return capabilities.VisionAnswer{}, err
	}
	c.put(convID, key, ans)
	return ans, nil
}

// Clear drops the cached answers for a conversation (e.g. on conversation
// unload, alongside visionattach.Store.Clear).
func (c *CachingInspector) Clear(convID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.byConv, convID)
	c.mu.Unlock()
}

func (c *CachingInspector) get(convID, key string) (capabilities.VisionAnswer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byConv[convID]
	if m == nil {
		return capabilities.VisionAnswer{}, false
	}
	ans, ok := m[key]
	return ans, ok
}

func (c *CachingInspector) put(convID, key string, ans capabilities.VisionAnswer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byConv[convID]
	if m == nil {
		m = make(map[string]capabilities.VisionAnswer)
		c.byConv[convID] = m
	}
	m[key] = ans
}

// cacheKey normalizes the question conservatively for the key: trim, collapse
// internal whitespace, and lowercase, so "What COLOR?" and "what  color?" share
// a cache entry. The original question is preserved in the caller's envelope;
// only the key is normalized. image_id is part of the key so two images in one
// conversation never collide.
func cacheKey(imageID, question string) string {
	return imageID + "\x00" + strings.ToLower(strings.Join(strings.Fields(question), " "))
}
