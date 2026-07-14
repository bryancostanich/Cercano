package mistralrs

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"cercano/source/server/internal/localruntime"
)

// urlFilename returns the filename portion of a download URL (after the last
// slash), used to place each file on disk under its own name.
func urlFilename(u string) string {
	if i := lastSlash(u); i >= 0 {
		return u[i+1:]
	}
	return u
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
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

	models := make([]CuratedModel, 0, len(cat.Models))
	for _, m := range cat.Models {
		models = append(models, m)
	}
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
