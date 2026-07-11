// Package modelcatalog discovers llama.cpp-compatible GGUF models from the
// HuggingFace model index.
//
// It supersedes the Ollama-library scraping in the ollamacatalog package.
// HuggingFace is where GGUF files actually live, it exposes model metadata —
// crucially the GGUF architecture — over a plain JSON API, and it serves the
// weights as resumable HTTPS downloads with no OCI protocol and no daemon.
// Because the architecture is in the API response, every candidate is checked
// against the compatibility gate (llamacompat) for one JSON fetch, not a
// multi-gigabyte header read, so an incompatible model (e.g. qwen3-next) is
// never offered for download into llama-server.
//
// Two calls back the browse experience:
//
//	List:   GET /api/models?filter=gguf&sort=downloads&direction=-1&limit=N
//	        (ranked GGUF repos; we keep only trusted uploaders)
//	Detail: GET /api/models/<repo>?blobs=true
//	        (the gguf{architecture,context_length,chat_template} block plus
//	         siblings[] — each quant file with its size)
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

// trustedAuthors is the uploader allow-list for browse. The HF GGUF index is
// vast and full of broken or experimental community quants, so discovery keeps
// only repos from uploaders with a track record of correct, complete GGUFs.
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

// HFFile is one GGUF quant variant within a repo.
type HFFile struct {
	Name      string // e.g. "Qwen3-14B-Q4_K_M.gguf"
	SizeBytes int64
}

// HFModelDetail is a repo's GGUF metadata: the architecture the gate checks,
// tool capability, context length, and the quant files with sizes.
type HFModelDetail struct {
	Repo          string
	Architecture  string
	ContextLength int
	SupportsTools bool
	Files         []HFFile
}

// Compatible reports whether the pinned llama.cpp build can load this model —
// the gate that keeps incompatible architectures out of the browse download
// path.
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

// ListModels queries the GGUF index ranked by downloads and returns entries
// from trusted uploaders only, preserving the API's ranking order. limit
// bounds the query size (the pre-filter fetch); 0 uses a sane default.
func (c *Client) ListModels(ctx context.Context, limit int) ([]HFModel, error) {
	if limit <= 0 {
		limit = 100
	}
	q := url.Values{}
	q.Set("filter", "gguf")
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

// hfDetail mirrors the per-repo fields we need.
type hfDetail struct {
	ID   string `json:"id"`
	GGUF struct {
		Architecture  string `json:"architecture"`
		ContextLength int    `json:"context_length"`
		ChatTemplate  string `json:"chat_template"`
	} `json:"gguf"`
	Siblings []struct {
		RFilename string `json:"rfilename"`
		Size      int64  `json:"size"`
		LFS       struct {
			Size int64 `json:"size"`
		} `json:"lfs"`
	} `json:"siblings"`
}

// ModelDetail fetches one repo's GGUF metadata and quant file list. Tool
// capability is inferred from the chat template (it must describe tool calls),
// which is what the agent tiers require.
func (c *Client) ModelDetail(ctx context.Context, repo string) (HFModelDetail, error) {
	endpoint := c.base() + "/api/models/" + repo + "?blobs=true"
	var d hfDetail
	if err := c.getJSON(ctx, endpoint, &d); err != nil {
		return HFModelDetail{}, err
	}
	detail := HFModelDetail{
		Repo:          repo,
		Architecture:  d.GGUF.Architecture,
		ContextLength: d.GGUF.ContextLength,
		SupportsTools: templateSupportsTools(d.GGUF.ChatTemplate),
	}
	for _, s := range d.Siblings {
		if !strings.HasSuffix(strings.ToLower(s.RFilename), ".gguf") {
			continue
		}
		size := s.Size
		if size == 0 {
			size = s.LFS.Size
		}
		detail.Files = append(detail.Files, HFFile{Name: s.RFilename, SizeBytes: size})
	}
	return detail, nil
}

// templateSupportsTools reports whether a chat template describes tool calling
// — the marker that a model can drive the agent's tool loop.
func templateSupportsTools(tmpl string) bool {
	return strings.Contains(tmpl, "tool_call") || strings.Contains(tmpl, "<tools>")
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
