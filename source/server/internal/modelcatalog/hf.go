// Package modelcatalog discovers models from the HuggingFace model index —
// llama.cpp-compatible GGUF quants and, for the mistral.rs runtime, safetensors
// repos.
//
// HuggingFace exposes model metadata — crucially the architecture — over a
// plain JSON API and serves the weights as resumable HTTPS downloads with no
// OCI protocol and no daemon. Because the architecture is in the API response,
// every candidate is checked against the active runtime's compatibility gate
// for one JSON fetch, not a multi-gigabyte header read, so an incompatible
// model is never offered for download.
//
// The browse experience is format-aware. For GGUF:
//
//	List:   GET /api/models?filter=gguf&sort=downloads&direction=-1&limit=N
//	Detail: GET /api/models/<repo>?blobs=true  →  the gguf{architecture,
//	        context_length,chat_template} block plus the .gguf quant files.
//
// For safetensors (mistral.rs):
//
//	List:   GET /api/models?filter=safetensors&...
//	Detail: GET /api/models/<repo>?blobs=true  →  config{model_type,
//	        architectures,tokenizer_config.chat_template} plus every inference
//	        file (weights + config + tokenizer) as the download manifest.
//
// A file's download URL is the plain resolve path:
//
//	https://huggingface.co/<repo>/resolve/main/<file>
package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"cercano/source/server/internal/llamacompat"
)

const defaultHFBaseURL = "https://huggingface.co"

// userAgent identifies Cercano to HuggingFace so their ops can distinguish
// tooling from browser traffic.
const userAgent = "cercano-modelcatalog/1.0"

// trustedAuthors is the uploader allow-list for browse. The HF index is vast
// and full of broken or experimental community uploads, so discovery keeps
// only repos from uploaders with a track record of correct, complete models.
// Growing this is a data edit. Keys are lowercase; author match is
// case-insensitive.
var trustedAuthors = map[string]struct{}{
	"ggml-org":   {}, // the llama.cpp org itself — gold standard
	"bartowski":  {},
	"unsloth":    {},
	"nomic-ai":   {}, // embeddings
	"qwen":       {}, // first-party
	"google":     {}, // first-party (gemma)
	"microsoft":  {}, // first-party (phi)
	"mistralai":  {}, // first-party
	"meta-llama": {}, // first-party
}

// HFModel is one entry from the list query — enough to rank and drill into.
type HFModel struct {
	Repo      string // "unsloth/Qwen3-14B-GGUF"
	Author    string // derived from Repo
	Downloads int
	Likes     int
}

// HFFile is one file within a repo (a GGUF quant, a safetensors shard, or a
// manifest sidecar like config.json).
type HFFile struct {
	Name      string // e.g. "Qwen3-14B-Q4_K_M.gguf" or "model-00001-of-00003.safetensors"
	SizeBytes int64
}

// HFModelDetail is a repo's metadata: the architecture the gate checks, tool
// capability, context length, and the files with sizes.
type HFModelDetail struct {
	Repo          string
	Format        string // "gguf" | "safetensors" (auto-detected from the API)
	Architecture  string
	ContextLength int
	SupportsTools bool
	// Files are the weight variants: the .gguf quants for a GGUF repo, or the
	// .safetensors shards for a safetensors repo.
	Files []HFFile
	// Manifest is every inference file for a safetensors repo (weights + config
	// + tokenizer), with docs/license/image junk excluded — the whole set a
	// directory-loaded runtime needs. Empty for a GGUF repo.
	Manifest []HFFile
}

// Compatible reports whether the pinned llama.cpp build can load this model.
// It is a GGUF convenience check (llamacompat); the runtime-aware gate the
// server applies is the authority for a safetensors/mistral.rs download.
func (d HFModelDetail) Compatible() bool {
	return llamacompat.Supported(d.Architecture)
}

// DownloadURL builds the plain resolve URL for a file in a repo. This is a
// direct HTTPS GET (Range-resumable), consumed by the download manager's
// normal path — no OCI, no manifest.
func DownloadURL(baseURL, repo, file string) string {
	if baseURL == "" {
		baseURL = defaultHFBaseURL
	}
	return baseURL + "/" + repo + "/resolve/main/" + file
}

// Client talks to the HuggingFace model API. The zero value is usable
// (default base URL, http.DefaultClient); tests point BaseURL at an httptest
// server.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultHFBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// authorOf returns the uploader for a repo id ("author/name" -> "author").
func authorOf(repo string) string {
	if i := strings.Index(repo, "/"); i >= 0 {
		return repo[:i]
	}
	return ""
}

// trusted reports whether a repo's author is on the allow-list.
func trusted(repo string) bool {
	_, ok := trustedAuthors[strings.ToLower(authorOf(repo))]
	return ok
}

// hfListEntry mirrors the fields we read from the list query. Author is
// derived from ID rather than trusted from the payload.
type hfListEntry struct {
	ID        string `json:"id"`
	Downloads int    `json:"downloads"`
	Likes     int    `json:"likes"`
}

// ListModels queries the index for the given format ("gguf" default, or
// "safetensors"), ranked by downloads, and returns entries from trusted
// uploaders only, preserving the API's ranking order. limit bounds the query
// size (the pre-filter fetch); 0 uses a sane default.
func (c *Client) ListModels(ctx context.Context, limit int, format string) ([]HFModel, error) {
	if limit <= 0 {
		limit = 100
	}
	if strings.TrimSpace(format) == "" {
		format = "gguf"
	}
	q := url.Values{}
	q.Set("filter", format)
	q.Set("sort", "downloads")
	q.Set("direction", "-1")
	q.Set("limit", fmt.Sprintf("%d", limit))
	endpoint := c.base() + "/api/models?" + q.Encode()

	var entries []hfListEntry
	if err := c.getJSON(ctx, endpoint, &entries); err != nil {
		return nil, err
	}
	out := make([]HFModel, 0, len(entries))
	for _, e := range entries {
		if !trusted(e.ID) {
			continue
		}
		out = append(out, HFModel{
			Repo:      e.ID,
			Author:    authorOf(e.ID),
			Downloads: e.Downloads,
			Likes:     e.Likes,
		})
	}
	return out, nil
}

// hfDetail mirrors the per-repo fields we need. A GGUF repo populates gguf{};
// a safetensors repo populates config{}.
type hfDetail struct {
	ID   string `json:"id"`
	GGUF struct {
		Architecture  string `json:"architecture"`
		ContextLength int    `json:"context_length"`
		ChatTemplate  string `json:"chat_template"`
	} `json:"gguf"`
	Config struct {
		ModelType       string   `json:"model_type"`
		Architectures   []string `json:"architectures"`
		TokenizerConfig struct {
			ChatTemplate string `json:"chat_template"`
		} `json:"tokenizer_config"`
	} `json:"config"`
	Siblings []struct {
		RFilename string `json:"rfilename"`
		Size      int64  `json:"size"`
		LFS       struct {
			Size int64 `json:"size"`
		} `json:"lfs"`
	} `json:"siblings"`
}

// ModelDetail fetches one repo's metadata and file list, auto-detecting the
// format from the API shape: a GGUF repo carries the gguf{} block, a
// safetensors repo carries config{} with a model_type. Tool capability is
// inferred from the chat template (it must describe tool calls). For a
// safetensors repo the full inference manifest is collected so a
// directory-loaded runtime gets weights, config, and tokenizer together.
func (c *Client) ModelDetail(ctx context.Context, repo string) (HFModelDetail, error) {
	endpoint := c.base() + "/api/models/" + repo + "?blobs=true"
	var d hfDetail
	if err := c.getJSON(ctx, endpoint, &d); err != nil {
		return HFModelDetail{}, err
	}
	detail := HFModelDetail{Repo: repo, ContextLength: d.GGUF.ContextLength}

	if d.GGUF.Architecture != "" {
		detail.Format = "gguf"
		detail.Architecture = d.GGUF.Architecture
		detail.SupportsTools = templateSupportsTools(d.GGUF.ChatTemplate)
		for _, s := range d.Siblings {
			if !strings.HasSuffix(strings.ToLower(s.RFilename), ".gguf") {
				continue
			}
			detail.Files = append(detail.Files, HFFile{Name: s.RFilename, SizeBytes: siblingSize(s.Size, s.LFS.Size)})
		}
		return detail, nil
	}

	detail.Format = "safetensors"
	detail.Architecture = d.Config.ModelType
	detail.SupportsTools = templateSupportsTools(d.Config.TokenizerConfig.ChatTemplate)
	for _, s := range d.Siblings {
		size := siblingSize(s.Size, s.LFS.Size)
		if strings.HasSuffix(strings.ToLower(s.RFilename), ".safetensors") {
			detail.Files = append(detail.Files, HFFile{Name: s.RFilename, SizeBytes: size})
		}
		if isManifestFile(s.RFilename) {
			detail.Manifest = append(detail.Manifest, HFFile{Name: s.RFilename, SizeBytes: size})
		}
	}
	return detail, nil
}

// siblingSize prefers the plain size, falling back to the LFS size (large
// files report their real size only under lfs).
func siblingSize(size, lfsSize int64) int64 {
	if size == 0 {
		return lfsSize
	}
	return size
}

// templateSupportsTools reports whether a chat template describes tool calling
// — the marker that a model can drive the agent's tool loop.
func templateSupportsTools(tmpl string) bool {
	return strings.Contains(tmpl, "tool_call") || strings.Contains(tmpl, "<tools>")
}

// isManifestFile reports whether a repo file belongs in a safetensors model's
// inference manifest — the weights, config, and tokenizer a directory-loaded
// runtime needs — excluding git metadata, license, docs, and image junk.
// merges.txt (a .txt tokenizer file) is deliberately kept.
func isManifestFile(name string) bool {
	switch strings.ToLower(name) {
	case ".gitattributes", ".gitignore", "license", "notice", "readme.md":
		return false
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".md", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".pdf"} {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return true
}

func (c *Client) getJSON(ctx context.Context, endpoint string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("modelcatalog: fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("modelcatalog: %s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("modelcatalog: decode %s: %w", endpoint, err)
	}
	return nil
}
