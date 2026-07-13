package mistralrs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cercano/source/server/internal/localruntime"
)

// catalogJSON is the curated mistral.rs catalog — safetensors/UQFF/GGUF builds
// verified to load on the pinned mistral.rs (v0.9.0). It is the foolproof model
// source (the setup wizard draws from it): nothing incompatible or
// Metal-unstable is listed. Browsing arbitrary HuggingFace repos is a separate,
// gated path. Deliberately conservative on Apple Silicon — hybrid-MoE families
// (qwen3next) stay out until the upstream Metal fixes release, even though the
// arch gate would admit them.
//
//go:embed catalog.json
var catalogJSON []byte

// CuratedModel is one curated model. Files is the full download manifest: for
// safetensors, every file mistral.rs needs (config.json, the model-*.safetensors
// shards + index, tokenizer files); for UQFF, the .uqff plus its config and
// tokenizer; for GGUF, the single .gguf. SizeBytes is the sum across files.
type CuratedModel struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Repo          string   `json:"repo"`
	Files         []string `json:"files"`
	Format        string   `json:"format"` // "safetensors" | "uqff" | "gguf"
	Architecture  string   `json:"arch"`   // mistral.rs model_type (mistralrscompat gate input)
	Family        string   `json:"family"`
	SizeBytes     int64    `json:"size_bytes"`
	SupportsTools bool     `json:"supports_tools"`
}

// DownloadURLs returns the HuggingFace resolve URL for each manifest file, in
// listed order. Plain Range-resumable HTTPS GETs the download manager consumes
// directly — the first file anchors the model's Path (its directory is where
// every file lands).
func (m CuratedModel) DownloadURLs() []string {
	out := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		out = append(out, "https://huggingface.co/"+m.Repo+"/resolve/main/"+f)
	}
	return out
}

// CuratedCatalog is the parsed catalog.json: a flat list of curated models.
// (RAM-tier profiles, like llama-server's, are a later wizard concern.)
type CuratedCatalog struct {
	Models []CuratedModel `json:"models"`
}

// loadCatalog parses the embedded catalog.json and checks basic integrity:
// every model has an id, a repo, at least one file, a known format, and a
// unique id. It does NOT check architecture compatibility — that is the gate's
// concern, asserted by the catalog validity test against mistralrscompat so a
// bad entry fails the build, not a user's setup. An empty catalog is valid.
func loadCatalog() (CuratedCatalog, error) {
	var cat CuratedCatalog
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		return CuratedCatalog{}, fmt.Errorf("parse catalog.json: %w", err)
	}
	seen := make(map[string]bool, len(cat.Models))
	for _, m := range cat.Models {
		if m.ID == "" || m.Repo == "" || len(m.Files) == 0 {
			return CuratedCatalog{}, fmt.Errorf("catalog model %q missing id/repo/files", m.ID)
		}
		switch m.Format {
		case "safetensors", "uqff", "gguf":
		default:
			return CuratedCatalog{}, fmt.Errorf("catalog model %q has unknown format %q", m.ID, m.Format)
		}
		if seen[m.ID] {
			return CuratedCatalog{}, fmt.Errorf("catalog has duplicate model id %q", m.ID)
		}
		seen[m.ID] = true
	}
	return cat, nil
}

// urlFilename returns the filename portion of a download URL (after the last
// slash), used to place each file on disk under its own name.
func urlFilename(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// allFilesPresent reports whether every manifest file of a model is present in
// dir — the "downloaded" test for a curated model.
func allFilesPresent(dir string, urls []string) bool {
	if len(urls) == 0 {
		return false
	}
	for _, u := range urls {
		info, err := os.Stat(filepath.Join(dir, urlFilename(u)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// catalogTargetDir resolves the directory curated downloads land in: the first
// configured model dir (with a leading ~ expanded), or ~/.cercano/models.
func (p *Provider) catalogTargetDir() string {
	if len(p.cfg.ModelDirs) > 0 {
		if expanded, err := expandPath(p.cfg.ModelDirs[0]); err == nil && expanded != "" {
			return expanded
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".cercano", "models")
}

// catalogModels surfaces the embedded curated catalog as downloadable model
// records. Each model's files land in its own subdirectory (the uniform
// per-model layout); for a directory-loaded format (safetensors/uqff)
// LoadTarget is that subdirectory (mistral.rs is launched with `-m <dir>`),
// while Path anchors the download on the first file. A model counts as
// downloaded only when every manifest file is present. Ordered by id.
func (p *Provider) catalogModels() []localruntime.ModelRecord {
	cat, err := loadCatalog()
	if err != nil {
		// A malformed embedded catalog is a build-time bug (the validity test
		// guards it); at runtime surface nothing rather than crash Discover.
		return nil
	}
	targetDir := p.catalogTargetDir()
	models := make([]CuratedModel, len(cat.Models))
	copy(models, cat.Models)
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

	out := make([]localruntime.ModelRecord, 0, len(models))
	for _, m := range models {
		urls := m.DownloadURLs()
		if len(urls) == 0 {
			continue
		}
		modelSub := filepath.Join(targetDir, localruntime.ModelDirName(m.ID))
		primary := filepath.Join(modelSub, urlFilename(urls[0]))
		loadTarget := ""
		if m.Format == "safetensors" || m.Format == "uqff" {
			// Directory-loaded: mistral.rs is pointed at the model dir.
			loadTarget = modelSub
		}
		state := "not_downloaded"
		var modified time.Time
		if allFilesPresent(modelSub, urls) {
			state = "downloaded"
			if info, statErr := os.Stat(primary); statErr == nil {
				modified = info.ModTime()
			}
		}
		out = append(out, localruntime.ModelRecord{
			ID:                 runtimeName + ":catalog:" + m.ID,
			DisplayName:        m.DisplayName,
			Runtime:            runtimeName,
			Source:             "catalog",
			Path:               primary,
			LoadTarget:         loadTarget,
			Format:             m.Format,
			Family:             m.Family,
			SizeBytes:          m.SizeBytes,
			ModifiedAt:         modified,
			DownloadState:      state,
			DownloadURLs:       urls,
			DownloadTotalBytes: m.SizeBytes,
			RuntimeState:       localruntime.StateStopped,
			SupportsChat:       true,
			SupportsTools:      m.SupportsTools,
		})
	}
	return out
}
