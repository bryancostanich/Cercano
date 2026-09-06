package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func servedModel() agentclient.RuntimeModel {
	return agentclient.RuntimeModel{
		ID:            "deepinfra:served:zai-org/GLM-5.3-Flash",
		DisplayName:   "zai-org/GLM-5.3-Flash",
		Source:        "catalog-served",
		Family:        "zai-org/GLM-5.3-Flash",
		CatalogID:     "zai-org/GLM-5.3-Flash",
		CatalogSource: "deepinfra",
		Acquisition:   agentclient.AcquisitionServe,
		Publisher:     "zai-org",
		Kind:          "text-generation",
		SupportsChat:  true,
		SupportsTools: true,
		PriceIn:       1_500_000,
		PriceOut:      5_000_000,
		ContextLength: 1048576,
	}
}

func downloadableModel() agentclient.RuntimeModel {
	return agentclient.RuntimeModel{
		ID:            "llama_server:online:TheBloke/Mistral-7B-GGUF",
		DisplayName:   "TheBloke/Mistral-7B-GGUF",
		Runtime:       "llama_server",
		Source:        "catalog-online",
		Format:        "gguf",
		Family:        "TheBloke/Mistral-7B-GGUF",
		CatalogID:     "TheBloke/Mistral-7B-GGUF",
		CatalogSource: "huggingface",
		Acquisition:   agentclient.AcquisitionDownload,
		DownloadState: "not_downloaded",
	}
}

// newDashboardWith builds a dashboard whose catalog holds exactly the given
// models, so cursor-driven actions operate on a known selection.
func newDashboardWith(models ...agentclient.RuntimeModel) *runtimeDashboard {
	d := &runtimeDashboard{}
	d.snapshot.Catalog = agentclient.RuntimeModelCatalog{Models: models}
	d.catalogCursor = 0
	return d
}

// TestServedAndDownloadable covers the accessor pair, including the
// compatibility case that matters most: a server predating the acquisition
// field sends "", and that must keep meaning downloadable.
func TestServedAndDownloadable(t *testing.T) {
	for _, tc := range []struct {
		name           string
		acquisition    string
		wantServed     bool
		wantDownloadab bool
	}{
		{"served", agentclient.AcquisitionServe, true, false},
		{"download", agentclient.AcquisitionDownload, false, true},
		{"empty from an older server means download", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := agentclient.RuntimeModel{Acquisition: tc.acquisition}
			if got := m.Served(); got != tc.wantServed {
				t.Errorf("Served() = %v, want %v", got, tc.wantServed)
			}
			if got := m.Downloadable(); got != tc.wantDownloadab {
				t.Errorf("Downloadable() = %v, want %v", got, tc.wantDownloadab)
			}
		})
	}
}

// TestIsCatalogDownloadModel_IncludesServed: a served model has no download
// lifecycle, so the download-state switch would have dropped it from browse
// entirely — it would never appear in the picker at all.
func TestIsCatalogDownloadModel_IncludesServed(t *testing.T) {
	if !isCatalogDownloadModel(servedModel()) {
		t.Error("served model excluded from the catalog list; it would never be shown")
	}
	if !isCatalogDownloadModel(downloadableModel()) {
		t.Error("downloadable model excluded from the catalog list")
	}
}

// TestFilteredCatalogModels_ServedSurvivesFiltering is the same claim through
// the real filter entry point rather than the predicate alone.
func TestFilteredCatalogModels_ServedSurvivesFiltering(t *testing.T) {
	models := []agentclient.RuntimeModel{downloadableModel(), servedModel()}
	got := filteredCatalogModels(models, "")
	if len(got) != 2 {
		t.Fatalf("filtered to %d models, want 2 (both should be browsable)", len(got))
	}
}

// TestCatalogModelMatches_ServedSearchableByProviderAndPublisher: a served
// model has no format or quantization to search on, so provider and publisher
// are how a user finds it.
func TestCatalogModelMatches_ServedSearchableByProviderAndPublisher(t *testing.T) {
	m := servedModel()
	for _, q := range []string{"deepinfra", "zai-org", "glm"} {
		if !catalogModelMatches(m, q) {
			t.Errorf("query %q did not match the served model", q)
		}
	}
	if catalogModelMatches(m, "definitely-not-present") {
		t.Error("unrelated query matched")
	}
}

// TestStartSelectedDownload_RefusesServedModel is the user-visible guarantee
// of Step 7: pressing download on a served model must refuse locally with a
// plain explanation rather than issuing a request the server will reject.
func TestStartSelectedDownload_RefusesServedModel(t *testing.T) {
	d := newDashboardWith(servedModel())

	cmd, ok := d.startSelectedDownload()
	if ok {
		t.Error("startSelectedDownload reported success for a served model")
	}
	if cmd != nil {
		t.Error("a download command was issued for a served model")
	}
	if d.catalogMessage == "" {
		t.Fatal("no message explaining the refusal")
	}
	if !strings.Contains(d.catalogMessage, "deepinfra") || !strings.Contains(d.catalogMessage, "nothing to download") {
		t.Errorf("message %q should name the provider and say there is nothing to download", d.catalogMessage)
	}
}

// TestStartSelectedDownload_AllowsDownloadableModel is the counterpart: the
// new guard must not block the normal path.
func TestStartSelectedDownload_AllowsDownloadableModel(t *testing.T) {
	d := newDashboardWith(downloadableModel())

	cmd, _ := d.startSelectedDownload()
	if cmd == nil {
		t.Error("no download command issued for a downloadable model")
	}
	if d.catalogMessage != "starting download..." {
		t.Errorf("message = %q, want \"starting download...\"", d.catalogMessage)
	}
}
