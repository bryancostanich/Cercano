package server

import (
	"testing"

	"cercano/source/server/internal/catalog"
)

// TestCatalogModelToProto_DownloadableKeepsRuntimeFields pins the pre-existing
// shape for a downloadable model. This is the backward-compatibility half:
// the CLI's models page reads runtime/format/download_state to decide it can
// be fetched, so a hosted source joining browse must not disturb them.
func TestCatalogModelToProto_DownloadableKeepsRuntimeFields(t *testing.T) {
	m := catalog.Model{
		Source:    "huggingface",
		ID:        "TheBloke/Mistral-7B-GGUF",
		Publisher: "TheBloke",
		Kind:      catalog.KindTextGeneration,
	}
	got := catalogModelToProto(m, false)

	if got.GetRuntime() != "llama_server" {
		t.Errorf("Runtime = %q, want llama_server", got.GetRuntime())
	}
	if got.GetFormat() != "gguf" {
		t.Errorf("Format = %q, want gguf", got.GetFormat())
	}
	if got.GetId() != "llama_server:online:"+m.ID {
		t.Errorf("Id = %q", got.GetId())
	}
	if got.GetDownloadState() == "" {
		t.Error("DownloadState empty; a downloadable model has a download to be in a state of")
	}
	if got.GetAcquisition() != acquisitionDownload {
		t.Errorf("Acquisition = %q, want %q", got.GetAcquisition(), acquisitionDownload)
	}
	if got.GetCatalogSource() != "huggingface" || got.GetCatalogId() != m.ID {
		t.Errorf("ref = %q/%q, want huggingface/%s", got.GetCatalogSource(), got.GetCatalogId(), m.ID)
	}
}

// TestCatalogModelToProto_ServedOmitsRuntimeFields is the core Step 7
// assertion. A served model has no local runtime, no on-disk format, and no
// download; claiming otherwise would put a download affordance on something
// that cannot be downloaded.
func TestCatalogModelToProto_ServedOmitsRuntimeFields(t *testing.T) {
	m := catalog.Model{
		Source:        "deepinfra",
		ID:            "zai-org/GLM-5.3-Flash",
		Publisher:     "zai-org",
		Kind:          catalog.KindTextGeneration,
		SupportsTools: true,
		ContextLength: 1048576,
		PriceIn:       1_500_000,
		PriceOut:      5_000_000,
	}
	got := catalogModelToProto(m, true)

	if got.GetRuntime() != "" {
		t.Errorf("Runtime = %q, want empty — a served model is not run locally", got.GetRuntime())
	}
	if got.GetFormat() != "" {
		t.Errorf("Format = %q, want empty — a served model has no on-disk format", got.GetFormat())
	}
	if got.GetDownloadState() != "" {
		t.Errorf("DownloadState = %q, want empty — there is no download", got.GetDownloadState())
	}
	if got.GetAcquisition() != acquisitionServe {
		t.Errorf("Acquisition = %q, want %q", got.GetAcquisition(), acquisitionServe)
	}
	if got.GetPriceIn() != 1_500_000 || got.GetPriceOut() != 5_000_000 {
		t.Errorf("pricing = %d/%d, want 1500000/5000000", got.GetPriceIn(), got.GetPriceOut())
	}
	if got.GetContextLength() != 1048576 {
		t.Errorf("ContextLength = %d", got.GetContextLength())
	}
	if got.GetPublisher() != "zai-org" {
		t.Errorf("Publisher = %q", got.GetPublisher())
	}
	if !got.GetSupportsTools() {
		t.Error("SupportsTools = false")
	}
	if got.GetCatalogSource() != "deepinfra" {
		t.Errorf("CatalogSource = %q, want deepinfra", got.GetCatalogSource())
	}
}

// TestCatalogModelToProto_ServedIdIsSourceScoped: the wire id must not collide
// across sources. Two providers can serve models with the same name, and the
// old "llama_server:online:"+ID prefix would have made them indistinguishable.
func TestCatalogModelToProto_ServedIdIsSourceScoped(t *testing.T) {
	m := catalog.Model{Source: "deepinfra", ID: "openai/gpt-oss-120b"}
	got := catalogModelToProto(m, true)

	want := "deepinfra:served:openai/gpt-oss-120b"
	if got.GetId() != want {
		t.Errorf("Id = %q, want %q", got.GetId(), want)
	}
	if got.GetId() == "llama_server:online:"+m.ID {
		t.Error("served model carries the local-runtime id prefix")
	}
}

// TestCatalogModelToProto_DeprecationTravels: sources filter retired models
// out of browse, so these fields exist for the case that matters — explaining
// a model pinned in config that has since died.
func TestCatalogModelToProto_DeprecationTravels(t *testing.T) {
	m := catalog.Model{
		Source:     "deepinfra",
		ID:         "zai-org/GLM-4.5-Air",
		Deprecated: true,
		ReplacedBy: "zai-org/GLM-5.3-Flash",
	}
	got := catalogModelToProto(m, true)

	if !got.GetDeprecated() {
		t.Error("Deprecated = false, want true")
	}
	if got.GetReplacedBy() != "zai-org/GLM-5.3-Flash" {
		t.Errorf("ReplacedBy = %q", got.GetReplacedBy())
	}
}

// TestCatalogModelToProto_DownloadableHasNoPrice: a downloadable model's cost
// is hardware, not per-token. A nonzero price here would misreport a free
// local model as metered.
func TestCatalogModelToProto_DownloadableHasNoPrice(t *testing.T) {
	m := catalog.Model{Source: "huggingface", ID: "some/repo"}
	got := catalogModelToProto(m, false)

	if got.GetPriceIn() != 0 || got.GetPriceOut() != 0 {
		t.Errorf("price = %d/%d, want 0/0 for a downloadable model", got.GetPriceIn(), got.GetPriceOut())
	}
}
