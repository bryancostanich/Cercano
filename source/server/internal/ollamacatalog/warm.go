// Package ollamacatalog — warm.go: background estimate pre-resolution.
//
// Instead of resolving RAM estimates lazily as the user scrolls the
// catalog (which serializes registry round-trips behind cursor
// movement and invites rate limiting), a background goroutine walks
// the catalog after each refresh and resolves every missing estimate,
// throttled to one registry hit every few hundred milliseconds. The
// digest-keyed cache makes this a one-time cost per model — a full
// cold warm of Ollama's ~236-family library moves ~60 MB total and
// finishes in about a minute; subsequent cycles only touch new
// catalog entries.
//
// Failures are NOT cached (a transient network error shouldn't leave a
// permanent hole), but attempts are tracked in-memory with a backoff
// so a persistently broken entry can't hammer the registry every wake.
package ollamacatalog

import (
	"context"
	"strings"
	"time"
)

const (
	// defaultWarmThrottle spaces out registry hits during a warm pass.
	defaultWarmThrottle = 250 * time.Millisecond
	// defaultWarmWake is how often the warmer looks for missing
	// estimates. Kept short so a fresh catalog (or server start with a
	// cold cache) starts warming promptly; passes where everything is
	// cached cost no network at all.
	defaultWarmWake = 15 * time.Second
	// warmRetryBackoff is how long a failed ref is left alone before
	// the warmer tries it again.
	warmRetryBackoff = time.Hour
)

// EnsureTag normalizes a bare family name to its ":latest" tag —
// the same default the download path uses.
func EnsureTag(ref string) string {
	if strings.Contains(ref, ":") {
		return ref
	}
	return ref + ":latest"
}

// warmLoop is the background goroutine body. Exits on ctx cancel or
// Stop().
func (m *Manager) warmLoop(ctx context.Context, stop chan struct{}) {
	timer := time.NewTimer(m.warmWakeInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C:
		}
		m.WarmMissingEstimates(ctx, stop)
		timer.Reset(m.warmWakeInterval())
	}
}

// WarmMissingEstimates makes one pass over the cached catalog and
// resolves every estimate that is missing (or expired with a moved
// digest). Returns how many estimates were newly resolved. Respects
// ctx/stop between every network hit, so shutdown never waits on the
// registry.
func (m *Manager) WarmMissingEstimates(ctx context.Context, stop <-chan struct{}) int {
	warmed := 0
	for _, mod := range m.Models() {
		if warmInterrupted(ctx, stop) {
			return warmed
		}
		ref := EnsureTag(mod.Name)
		if _, ok := m.cachedEstimate(ref, time.Now()); ok {
			continue
		}
		if !m.shouldAttemptWarm(ref) {
			continue
		}
		m.recordWarmAttempt(ref)
		if _, err := m.ResolveEstimate(ctx, ref); err == nil {
			warmed++
		}
		if !warmSleep(ctx, stop, m.warmThrottleInterval()) {
			return warmed
		}
	}
	return warmed
}

// shouldAttemptWarm reports whether a ref is outside its failure
// backoff window. Refs that resolved successfully never get here —
// the cache check in the warm pass short-circuits first.
func (m *Manager) shouldAttemptWarm(ref string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	last, ok := m.warmAttempted[ref]
	return !ok || time.Since(last) > warmRetryBackoff
}

func (m *Manager) recordWarmAttempt(ref string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.warmAttempted == nil {
		m.warmAttempted = make(map[string]time.Time)
	}
	m.warmAttempted[ref] = time.Now()
}

func (m *Manager) warmThrottleInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.warmThrottle > 0 {
		return m.warmThrottle
	}
	return defaultWarmThrottle
}

func (m *Manager) warmWakeInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.warmWake > 0 {
		return m.warmWake
	}
	return defaultWarmWake
}

// SetWarmIntervals overrides the warm cadence — tests use effectively
// zero values to run passes synchronously fast.
func (m *Manager) SetWarmIntervals(throttle, wake time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warmThrottle = throttle
	m.warmWake = wake
}

func warmInterrupted(ctx context.Context, stop <-chan struct{}) bool {
	select {
	case <-ctx.Done():
		return true
	case <-stop:
		return true
	default:
		return false
	}
}

// warmSleep waits out the throttle; false means shutdown interrupted
// the wait.
func warmSleep(ctx context.Context, stop <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		return !warmInterrupted(ctx, stop)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-t.C:
		return true
	}
}
