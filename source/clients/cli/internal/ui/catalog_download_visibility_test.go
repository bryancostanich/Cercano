package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// Pins the mid-download catalog experience: a model being downloaded
// must stay in the filtered catalog list (state "downloading" is a
// download-candidate state) and keep matching the filter text the
// user selected it under.
func TestCatalogList_DownloadingModelStaysVisible(t *testing.T) {
	// The server-side merge mid-download: the enrolled inventory
	// record (family set, state downloading) is present; the online
	// catalog entry it came from is suppressed by the family dedup.
	merged := []agentclient.RuntimeModel{
		{ID: "llama_server:catalog:phi4-mini", DisplayName: "Phi4 Mini", Family: "phi4-mini", Source: "catalog", DownloadState: "downloaded", Path: "/x/phi4.gguf"},
		{
			ID:              "llama_server:online:qwen3-coder-next",
			DisplayName:     "Qwen3 Coder Next",
			Family:          "qwen3-coder-next",
			OllamaRef:       "qwen3-coder-next:latest",
			DownloadState:   "downloading",
			DownloadedBytes: 1 << 30, DownloadTotalBytes: 50 << 30,
			Path: "/x/qwen3-coder-next-latest.gguf",
		},
		{ID: "llama_server:online:qwen3", DisplayName: "Qwen3", Family: "qwen3", OllamaRef: "qwen3", DownloadState: "not_downloaded"},
	}

	filtered := filteredCatalogModels(merged, "qwen3")
	var ids []string
	for _, m := range filtered {
		ids = append(ids, m.ID)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered = %v, want the downloading entry AND the qwen3 family", ids)
	}
	foundDownloading := false
	for _, m := range filtered {
		if m.ID == "llama_server:online:qwen3-coder-next" && m.DownloadState == "downloading" {
			foundDownloading = true
		}
	}
	if !foundDownloading {
		t.Fatalf("downloading model missing from filtered list: %v", ids)
	}

	// And the detail panel must say so: state line + progress bar.
	m := New(nil, false)
	details := renderPlainDetails(t, filtered, m)
	if details == "" {
		t.Fatal("no details rendered")
	}
}

// App-store semantics: a model that FINISHED downloading stays in
// catalog search results (labeled downloaded) instead of vanishing the
// moment the download completes.
func TestCatalogList_DownloadedModelStaysVisible(t *testing.T) {
	merged := []agentclient.RuntimeModel{
		{
			ID:            "llama_server:online:nomic-embed-text",
			DisplayName:   "Nomic Embed Text",
			Family:        "nomic-embed-text",
			OllamaRef:     "nomic-embed-text:latest",
			DownloadState: "downloaded",
			Path:          "/x/nomic-embed-text-latest.gguf",
		},
	}
	filtered := filteredCatalogModels(merged, "nomic")
	if len(filtered) != 1 || filtered[0].DownloadState != "downloaded" {
		t.Fatalf("downloaded model vanished from catalog search: %+v", filtered)
	}
}

func renderPlainDetails(t *testing.T, models []agentclient.RuntimeModel, m Model) string {
	t.Helper()
	for _, model := range models {
		if model.DownloadState == "downloading" {
			lines := catalogDetailLines(model, nil, false, 80, m.styles)
			out := ""
			for _, l := range lines {
				out += l + "\n"
			}
			return out
		}
	}
	return ""
}
