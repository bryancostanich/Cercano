package modelcatalog

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"cercano/source/server/internal/catalog"
)

// BackendName is the catalog backend identifier for HuggingFace.
const BackendName = "huggingface"

// Backend adapts the HuggingFace Client to catalog.Backend — the default
// active backend. HuggingFace is the real home of GGUF files, its API exposes
// the architecture the gate needs, and its downloads are plain resumable HTTPS
// with no manifest step.
type Backend struct {
	client *Client
}

// NewBackend wraps a Client; a nil client uses defaults.
func NewBackend(c *Client) *Backend {
	if c == nil {
		c = &Client{}
	}
	return &Backend{client: c}
}

// Name implements catalog.Backend.
func (b *Backend) Name() string { return BackendName }

// List implements catalog.Backend: the trusted-uploader-filtered GGUF index,
// optionally narrowed by a substring query on the repo id.
func (b *Backend) List(ctx context.Context, opts catalog.ListOptions) ([]catalog.Model, error) {
	models, err := b.client.ListModels(ctx, opts.Limit)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	out := make([]catalog.Model, 0, len(models))
	for _, m := range models {
		if query != "" && !strings.Contains(strings.ToLower(m.Repo), query) {
			continue
		}
		out = append(out, catalog.Model{
			Backend:   BackendName,
			ID:        m.Repo,
			Author:    m.Author,
			Downloads: m.Downloads,
			Likes:     m.Likes,
		})
	}
	return out, nil
}

// Detail implements catalog.Backend.
func (b *Backend) Detail(ctx context.Context, id string) (catalog.Detail, error) {
	d, err := b.client.ModelDetail(ctx, id)
	if err != nil {
		return catalog.Detail{}, err
	}
	return catalog.Detail{
		Backend:       BackendName,
		ID:            d.Repo,
		Architecture:  d.Architecture,
		ContextLength: d.ContextLength,
		SupportsTools: d.SupportsTools,
		Files:         toCatalogFiles(d.Files),
	}, nil
}

// ResolveDownload implements catalog.Backend: it turns a chosen quant into the
// plain resolve URL(s). A normal quant is one file; a sharded quant (a split
// GGUF) resolves to every shard in its group so the download manager fetches
// them all.
func (b *Backend) ResolveDownload(ctx context.Context, id, file string) (catalog.DownloadPlan, error) {
	d, err := b.client.ModelDetail(ctx, id)
	if err != nil {
		return catalog.DownloadPlan{}, err
	}
	group := filesForDownload(d.Files, file)
	if len(group) == 0 {
		return catalog.DownloadPlan{}, fmt.Errorf("modelcatalog: file %q not found in %s", file, id)
	}
	plan := catalog.DownloadPlan{PrimaryFile: group[0].Name}
	for _, f := range group {
		plan.URLs = append(plan.URLs, DownloadURL(b.client.base(), id, f.Name))
		plan.TotalBytes += f.SizeBytes
	}
	return plan, nil
}

func toCatalogFiles(in []HFFile) []catalog.File {
	out := make([]catalog.File, 0, len(in))
	for _, f := range in {
		out = append(out, catalog.File{Name: f.Name, SizeBytes: f.SizeBytes})
	}
	return out
}

// shardRE matches a sharded GGUF filename such as
// "Q4_K_M/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf" — capturing the prefix, the
// shard index, and the shard count.
var shardRE = regexp.MustCompile(`^(.*)-(\d+)-of-(\d+)\.gguf$`)

// filesForDownload returns the file(s) that make up the chosen quant: the one
// file for a normal quant, or every shard in its group (name-sorted, so
// 00001 precedes 00002) when the chosen file is one shard of a split.
func filesForDownload(files []HFFile, chosen string) []HFFile {
	m := shardRE.FindStringSubmatch(chosen)
	if m == nil {
		for _, f := range files {
			if f.Name == chosen {
				return []HFFile{f}
			}
		}
		return nil
	}
	prefix, total := m[1], m[3]
	var group []HFFile
	for _, f := range files {
		fm := shardRE.FindStringSubmatch(f.Name)
		if fm != nil && fm[1] == prefix && fm[3] == total {
			group = append(group, f)
		}
	}
	sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
	return group
}
