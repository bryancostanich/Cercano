package ollamacatalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cercano/source/server/internal/catalog"
	"cercano/source/server/internal/gguf"
)

// BackendName is the catalog backend identifier for Ollama.
const BackendName = "ollama"

// archReadWindow bounds the GGUF header Range read used to detect a model's
// architecture. The identity keys sit near the front of the file; 256 KiB is
// the same window the on-disk header parser uses.
const archReadWindow = 256 * 1024

// ollamaSource is the subset of *Manager the Backend needs. An interface so the
// adapter's mapping logic is testable with a stub, without standing up a fake
// Ollama registry. *Manager satisfies it.
type ollamaSource interface {
	Models() []Model
	ListTags(name string) ([]string, error)
	Resolve(ctx context.Context, ref string) (downloadURL string, sizeBytes int64, err error)
}

// Backend adapts the Ollama library to catalog.Backend — the retained,
// non-default backend. An Ollama model is a family of tags (its quant/size
// variants). The architecture the gate needs is not in Ollama's API, so Detail
// reads it from the GGUF header of a resolved blob; the OCI manifest→blob
// resolution that used to live in the download manager now lives here, in
// ResolveDownload, so the download manager stays backend-agnostic.
type Backend struct {
	src    ollamaSource
	client *http.Client
	// archReader reads a GGUF architecture from a blob URL. Injectable so the
	// mapping logic is testable without synthesizing GGUF bytes; the default
	// Range-GETs the header and parses it.
	archReader func(ctx context.Context, blobURL string) (string, error)
}

var _ catalog.Backend = (*Backend)(nil)

// NewBackend wraps an Ollama Manager as a catalog backend.
func NewBackend(mgr *Manager) *Backend {
	b := &Backend{src: mgr, client: http.DefaultClient}
	b.archReader = b.readArchFromBlob
	return b
}

// Name implements catalog.Backend.
func (b *Backend) Name() string { return BackendName }

// List implements catalog.Backend: the cached library families, optionally
// narrowed by a substring query on the family name.
func (b *Backend) List(_ context.Context, opts catalog.ListOptions) ([]catalog.Model, error) {
	families := b.src.Models()
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	out := make([]catalog.Model, 0, len(families))
	for _, f := range families {
		if query != "" && !strings.Contains(strings.ToLower(f.Name), query) {
			continue
		}
		out = append(out, catalog.Model{Backend: BackendName, ID: f.Name})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

// Detail implements catalog.Backend: a family's tags become the downloadable
// files, and the architecture is read from the first tag's blob header. The
// arch read is best-effort — on failure the arch is left empty, which the
// consumer's gate treats as unsupported (the safe default for an unknown arch).
func (b *Backend) Detail(ctx context.Context, id string) (catalog.Detail, error) {
	tags, err := b.src.ListTags(id)
	if err != nil {
		return catalog.Detail{}, err
	}
	if len(tags) == 0 {
		return catalog.Detail{}, fmt.Errorf("ollamacatalog: no tags for %q", id)
	}
	files := make([]catalog.File, 0, len(tags))
	for _, t := range tags {
		files = append(files, catalog.File{Name: t})
	}
	arch := ""
	if blobURL, _, rerr := b.src.Resolve(ctx, id+":"+tags[0]); rerr == nil {
		if a, aerr := b.archReader(ctx, blobURL); aerr == nil {
			arch = a
		}
	}
	return catalog.Detail{
		Backend:      BackendName,
		ID:           id,
		Architecture: arch,
		Files:        files,
	}, nil
}

// ResolveDownload implements catalog.Backend: it turns a family + tag into a
// concrete blob download via the OCI manifest→blob resolution, so the download
// manager just fetches the resulting URL.
func (b *Backend) ResolveDownload(ctx context.Context, id, file string) (catalog.DownloadPlan, error) {
	ref := id + ":" + file
	url, size, err := b.src.Resolve(ctx, ref)
	if err != nil {
		return catalog.DownloadPlan{}, err
	}
	// The ref isn't a filename; sanitize it into one for on-disk storage.
	primary := strings.ReplaceAll(ref, ":", "-") + ".gguf"
	return catalog.DownloadPlan{
		URLs:        []string{url},
		PrimaryFile: primary,
		TotalBytes:  size,
	}, nil
}

// readArchFromBlob Range-reads a blob's GGUF header and parses out the
// architecture.
func (b *Backend) readArchFromBlob(ctx context.Context, blobURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", archReadWindow-1))
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("ollamacatalog: arch read returned %s", resp.Status)
	}
	meta, err := gguf.ParseMeta(io.LimitReader(resp.Body, archReadWindow))
	if err != nil {
		return "", err
	}
	return meta.Architecture, nil
}
