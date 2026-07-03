// Package ollamacatalog — estimate.go: pre-download RAM estimation.
//
// Answers "will this model even fit on my machine?" before the user
// commits to a multi-gigabyte download. Two cheap fetches per model:
// the OCI manifest (~1 KiB, gives the exact GGUF blob size = weights
// RAM) and a ranged GET of the blob's first 256 KiB (the GGUF header,
// which carries the architecture keys that determine KV-cache cost —
// verified live against registry.ollama.ai, which honors Range).
//
// Caching: estimates are keyed by ref ("name:tag") in the same on-disk
// cache file as the catalog. Within the TTL a hit costs zero network.
// Past the TTL we re-fetch only the manifest — if the tag still points
// at the same digest (the common case; architectures never change
// under a digest) the stored estimate is reused and re-stamped.
package ollamacatalog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"cercano/source/server/internal/gguf"
)

// headerWindowBytes is how much of the blob we fetch to parse the GGUF
// header. 256 KiB comfortably covers the architecture keys, which sit
// ahead of the tokenizer arrays in every converter we've probed.
const headerWindowBytes = 256 * 1024

// Estimate holds what a client needs to predict runtime RAM for a
// model at arbitrary context sizes:
//
//	total(ctx) ≈ WeightsBytes + ctx*KVBytesPerToken + overhead
//
// JSON tags matter — Estimate is embedded in the on-disk cache file.
type Estimate struct {
	// Ref is the "name:tag" this estimate was resolved for.
	Ref string `json:"ref"`
	// Digest is the model layer's blob digest at resolve time. Used to
	// revalidate cheaply when the TTL lapses: same digest = same file =
	// same estimate.
	Digest string `json:"digest"`
	// WeightsBytes is the exact GGUF blob size from the manifest.
	WeightsBytes int64 `json:"weights_bytes"`
	// KVBytesPerToken is the f16 KV-cache cost of one context token.
	KVBytesPerToken int64 `json:"kv_bytes_per_token"`
	// MaxContextTokens is the model's trained context limit.
	MaxContextTokens int64 `json:"max_context_tokens"`
	// Architecture is informational (e.g. "qwen2").
	Architecture string `json:"architecture,omitempty"`
	// ResolvedAt drives TTL-based revalidation.
	ResolvedAt time.Time `json:"resolved_at"`
}

// FetchBlobWindow fetches the first n bytes of a blob via a bounded
// Range request. Falls back gracefully if the server ignores Range and
// replies 200 with the full body: the read is capped at n either way,
// so at worst we transfer a few extra packets before closing.
func (f *Fetcher) FetchBlobWindow(name, digest string, n int64) ([]byte, error) {
	url := fmt.Sprintf("%s/v2/library/%s/blobs/%s", f.registryURL(), name, digest)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", n-1))
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollamacatalog: fetch header window of %s: %w", digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("ollamacatalog: header window of %s returned %s", digest, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, n))
	if err != nil {
		return nil, fmt.Errorf("ollamacatalog: read header window of %s: %w", digest, err)
	}
	return data, nil
}

// ResolveEstimate returns the RAM-estimation numbers for an ollama ref
// ("name:tag" — same strictness as Resolve; callers normalize bare
// family names to :latest before calling). Network cost: zero on a
// fresh cache hit, one manifest fetch on a TTL-lapsed hit whose digest
// hasn't moved, manifest + 256 KiB range fetch otherwise.
func (m *Manager) ResolveEstimate(ctx context.Context, ref string) (Estimate, error) {
	name, tag, ok := splitOllamaRef(ref)
	if !ok {
		return Estimate{}, fmt.Errorf("ollamacatalog: invalid ollama ref %q (want name:tag)", ref)
	}
	if est, ok := m.cachedEstimate(ref, time.Now()); ok {
		return est, nil
	}
	manifest, err := m.fetcher.FetchManifest(name, tag)
	if err != nil {
		return Estimate{}, err
	}
	layer, err := manifest.ModelLayer()
	if err != nil {
		return Estimate{}, err
	}
	// TTL lapsed but the tag still points at the same blob: the file
	// content is immutable under a digest, so the old numbers stand.
	if est, ok := m.staleEstimateForDigest(ref, layer.Digest); ok {
		est.ResolvedAt = time.Now()
		m.storeEstimate(est)
		return est, nil
	}
	window, err := m.fetcher.FetchBlobWindow(name, layer.Digest, headerWindowBytes)
	if err != nil {
		return Estimate{}, err
	}
	meta, err := gguf.ParseMeta(bytes.NewReader(window))
	if err != nil {
		return Estimate{}, fmt.Errorf("ollamacatalog: parse header of %s: %w", ref, err)
	}
	est := Estimate{
		Ref:              ref,
		Digest:           layer.Digest,
		WeightsBytes:     layer.Size,
		KVBytesPerToken:  meta.KVBytesPerToken(),
		MaxContextTokens: int64(meta.ContextLength),
		Architecture:     meta.Architecture,
		ResolvedAt:       time.Now(),
	}
	m.storeEstimate(est)
	return est, nil
}

// cachedEstimate returns a fresh (within-TTL) estimate for ref.
func (m *Manager) cachedEstimate(ref string, now time.Time) (Estimate, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cache == nil {
		return Estimate{}, false
	}
	est, ok := m.cache.Estimates[ref]
	if !ok {
		return Estimate{}, false
	}
	if now.Sub(est.ResolvedAt) > m.ttl {
		return Estimate{}, false
	}
	return est, true
}

// staleEstimateForDigest returns the stored estimate for ref if it
// matches the given digest, regardless of age.
func (m *Manager) staleEstimateForDigest(ref, digest string) (Estimate, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cache == nil {
		return Estimate{}, false
	}
	est, ok := m.cache.Estimates[ref]
	if !ok || est.Digest != digest {
		return Estimate{}, false
	}
	return est, true
}

// storeEstimate writes the estimate into the in-memory cache and
// persists to disk. A persist failure is deliberately swallowed — the
// estimate is still served from memory for this process's lifetime,
// and the next resolve simply re-fetches.
func (m *Manager) storeEstimate(est Estimate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = &Cache{}
	}
	if m.cache.Estimates == nil {
		m.cache.Estimates = make(map[string]Estimate)
	}
	m.cache.Estimates[est.Ref] = est
	_ = m.cache.Save(m.cachePath)
}
