// Package ollamacatalog fetches the online catalog of models hosted by
// Ollama and provides a resumable HTTP client for downloading their raw
// GGUF blobs.
//
// The Ollama library at https://ollama.com/library is our chosen
// discovery source because:
//
//   - It's already Cercano's compatibility matrix — Ollama is one of
//     the two supported local runtimes, and models that appear there
//     are guaranteed to work with the runtime.
//   - The underlying blobs are raw GGUF files (verified: first four
//     bytes are the "GGUF" magic), which llama-server can also consume
//     directly with no reformatting.
//   - The registry at https://registry.ollama.ai serves everything via
//     standard OCI protocol — no auth, no rate limits for public
//     library models, and support for HTTP Range requests so downloads
//     are resumable.
//
// This package is deliberately not coupled to any daemon: fetching the
// catalog and downloading blobs are both plain HTTPS. Users of the
// llama-server runtime can pull models without ever having the ollama
// daemon installed.
//
// The three URL patterns we depend on:
//
//	Catalog:   GET https://ollama.com/library
//	           (HTML; scraped for /library/<name> links)
//	Tags:      GET https://ollama.com/library/<name>
//	           (HTML; scraped for /library/<name>:<tag> links)
//	Manifest:  GET https://registry.ollama.ai/v2/library/<name>/manifests/<tag>
//	           (OCI manifest JSON; layer with
//	            mediaType=application/vnd.ollama.image.model has the GGUF)
//	Blob:      GET https://registry.ollama.ai/v2/library/<name>/blobs/sha256:<hash>
//	           (raw GGUF file, Range requests supported)
package ollamacatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Defaults for the public library. Overridable via Fetcher.BaseURL /
// Fetcher.RegistryURL so tests can point at an httptest server without
// hitting the real network.
const (
	defaultLibraryBaseURL  = "https://ollama.com"
	defaultRegistryBaseURL = "https://registry.ollama.ai"
	// modelLayerMediaType is the OCI mediaType we look for in an ollama
	// manifest to find the actual GGUF blob (everything else — template,
	// system prompt, license — is metadata layers).
	modelLayerMediaType = "application/vnd.ollama.image.model"
	// userAgent identifies Cercano to the registry / library. Helps if
	// Ollama's ops team ever needs to distinguish organic browser
	// traffic from tooling in their logs.
	userAgent = "cercano-catalog/1.0"
)

// Model is one entry in the online catalog — a family name like "qwen3"
// or "llama3.2" and the tags available under it. Tags are populated
// lazily; a plain list of families comes back from ListModels, and each
// family's tags are fetched on demand by ListTags.
type Model struct {
	Name string   `json:"name"`
	Tags []string `json:"tags,omitempty"`
}

// ManifestLayer describes one layer of an OCI image manifest — used to
// pick out the GGUF blob from an ollama model.
type ManifestLayer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"` // "sha256:..."
	Size      int64  `json:"size"`
}

// Manifest is the subset of OCI image manifest fields we care about.
type Manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Layers        []ManifestLayer `json:"layers"`
}

// ModelLayer returns the layer holding the actual GGUF blob for this
// manifest, or an error if the manifest has no such layer.
func (m *Manifest) ModelLayer() (ManifestLayer, error) {
	for _, l := range m.Layers {
		if l.MediaType == modelLayerMediaType {
			return l, nil
		}
	}
	return ManifestLayer{}, fmt.Errorf("ollamacatalog: manifest has no %s layer", modelLayerMediaType)
}

// Fetcher is the HTTP client for the online catalog. Zero value is
// usable (uses default URLs and http.DefaultClient).
type Fetcher struct {
	// BaseURL is where the human-facing library lives (default:
	// https://ollama.com). Tests can point this at an httptest server.
	BaseURL string
	// RegistryURL is the OCI registry (default: https://registry.ollama.ai).
	RegistryURL string
	// HTTPClient is used for all fetches. Nil = http.DefaultClient.
	HTTPClient *http.Client
}

func (f *Fetcher) libraryURL() string {
	if f.BaseURL != "" {
		return f.BaseURL
	}
	return defaultLibraryBaseURL
}

func (f *Fetcher) registryURL() string {
	if f.RegistryURL != "" {
		return f.RegistryURL
	}
	return defaultRegistryBaseURL
}

func (f *Fetcher) client() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return http.DefaultClient
}

// libraryLinkRE matches href="/library/<name>" links on the library
// index page. Anchored so that name capture doesn't include a colon
// (which would be a tag, not a family) — those come from ListTags.
var libraryLinkRE = regexp.MustCompile(`href="/library/([^":/]+)"`)

// ListModels fetches https://ollama.com/library and extracts every
// unique model-family link on the page. Returns the sorted, deduped set.
//
// The list is stable across calls (Ollama updates their library
// infrequently), so callers should cache the result — see Cache.
func (f *Fetcher) ListModels() ([]Model, error) {
	req, err := http.NewRequest(http.MethodGet, f.libraryURL()+"/library", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollamacatalog: fetch library: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollamacatalog: library returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, m := range libraryLinkRE.FindAllSubmatch(body, -1) {
		seen[string(m[1])] = struct{}{}
	}
	out := make([]Model, 0, len(seen))
	for name := range seen {
		out = append(out, Model{Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// tagLinkRE matches href="/library/<name>:<tag>" links on a specific
// model's page. The name is captured for a sanity check (must match the
// requested family), and the tag is captured separately.
var tagLinkRE = regexp.MustCompile(`href="/library/([^":/]+):([^"]+)"`)

// ListTags fetches the tag list for one model family from
// https://ollama.com/library/<name>. Returns sorted, deduped tags.
func (f *Fetcher) ListTags(name string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, f.libraryURL()+"/library/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollamacatalog: fetch tags for %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollamacatalog: tags page for %s returned %s", name, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, m := range tagLinkRE.FindAllSubmatch(body, -1) {
		if string(m[1]) != name {
			continue // sanity — should not happen but guard against link injection
		}
		seen[string(m[2])] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// FetchManifest retrieves the OCI manifest for a specific model:tag.
// The manifest names the blob layers; callers typically then pick the
// model layer via Manifest.ModelLayer() and download it via DownloadBlob.
func (f *Fetcher) FetchManifest(name, tag string) (*Manifest, error) {
	url := fmt.Sprintf("%s/v2/library/%s/manifests/%s", f.registryURL(), name, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollamacatalog: fetch manifest %s:%s: %w", name, tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollamacatalog: manifest %s:%s returned %s", name, tag, resp.Status)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("ollamacatalog: decode manifest %s:%s: %w", name, tag, err)
	}
	return &m, nil
}

// DownloadBlob opens a stream of the model blob for the given digest.
// The caller is responsible for reading it to a file — the reader is
// resumable via HTTP Range requests, but this initial helper always
// returns the full stream (see DownloadBlobRange for resumption).
// Returns the blob's total size and a ReadCloser positioned at byte 0.
//
// Registered mediaType is application/octet-stream (verified against
// the live registry); the caller writes the bytes directly to disk with
// no interpretation. The blob IS the GGUF file — no unwrapping needed.
func (f *Fetcher) DownloadBlob(name, digest string) (io.ReadCloser, int64, error) {
	url := fmt.Sprintf("%s/v2/library/%s/blobs/%s", f.registryURL(), name, digest)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ollamacatalog: fetch blob %s: %w", digest, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("ollamacatalog: blob %s returned %s", digest, resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// DownloadBlobRange opens a stream starting at the given byte offset —
// used to resume a partial download after a network interruption. The
// registry supports Range requests (verified live), so setting offset=0
// is equivalent to DownloadBlob for a fresh pull.
func (f *Fetcher) DownloadBlobRange(name, digest string, offset int64) (io.ReadCloser, int64, error) {
	url := fmt.Sprintf("%s/v2/library/%s/blobs/%s", f.registryURL(), name, digest)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ollamacatalog: resume blob %s from %d: %w", digest, offset, err)
	}
	// 200 (full stream) or 206 (partial content) are both success paths.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("ollamacatalog: blob %s resume returned %s", digest, resp.Status)
	}
	// Total size is reported in Content-Range for 206 responses; for
	// 200s it's Content-Length. Normalize both to "total bytes remaining
	// in the stream" so the caller's progress accounting is simple.
	total := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		if idx := strings.LastIndex(cr, "/"); idx >= 0 {
			if _, err := fmt.Sscanf(cr[idx+1:], "%d", &total); err == nil && total > 0 {
				total = total - offset
			}
		}
	}
	return resp.Body, total, nil
}
