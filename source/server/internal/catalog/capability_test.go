package catalog_test

import (
	"context"
	"testing"

	"cercano/source/server/internal/catalog"
)

// servableOnly is a source whose models are served, never downloaded — the
// shape a hosted inference provider takes. It implements Source and nothing
// more.
type servableOnly struct{}

func (servableOnly) Name() string { return "servable" }
func (servableOnly) List(context.Context, catalog.ListOptions) ([]catalog.Model, error) {
	return nil, nil
}
func (servableOnly) Detail(context.Context, string) (catalog.Detail, error) {
	return catalog.Detail{}, nil
}

// downloadable is a source that yields local files.
type downloadable struct{ servableOnly }

func (downloadable) Name() string { return "downloadable" }
func (downloadable) ResolveDownload(context.Context, string, string) (catalog.DownloadPlan, error) {
	return catalog.DownloadPlan{}, nil
}

// TestServableSourceIsNotDownloadable is the load-bearing guarantee of the
// capability split: a source that only serves models must not satisfy
// Downloadable, so consumers that fetch bytes cannot be handed one. The
// compile-time half is enforced by buildCatalogDownloadRecord taking a
// Downloadable; this pins the runtime half that the type assertion guarding
// it actually discriminates.
func TestServableSourceIsNotDownloadable(t *testing.T) {
	var src catalog.Source = servableOnly{}
	if _, ok := src.(catalog.Downloadable); ok {
		t.Fatal("a servable-only source must not satisfy catalog.Downloadable")
	}
}

// TestDownloadableSourceSatisfiesBoth checks the split did not accidentally
// make Downloadable unsatisfiable: a file-yielding source is usable as both.
func TestDownloadableSourceSatisfiesBoth(t *testing.T) {
	var src catalog.Source = downloadable{}
	if _, ok := src.(catalog.Downloadable); !ok {
		t.Fatal("a file-yielding source must satisfy catalog.Downloadable")
	}
}
