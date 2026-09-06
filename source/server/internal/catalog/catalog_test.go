package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// downloadableSource is a Source whose models become local files. knows
// bounds which ids it recognizes, so Detail can fail the way a real source
// does for an id belonging to somebody else.
type downloadableSource struct {
	name  string
	knows []string
}

func (f downloadableSource) Name() string { return f.name }
func (f downloadableSource) List(context.Context, ListOptions) ([]Model, error) {
	out := make([]Model, 0, len(f.knows))
	for _, id := range f.knows {
		out = append(out, Model{Source: f.name, ID: id})
	}
	return out, nil
}
func (f downloadableSource) Detail(_ context.Context, id string) (Detail, error) {
	for _, k := range f.knows {
		if k == id {
			return Detail{Source: f.name, ID: id}, nil
		}
	}
	return Detail{}, errors.New("not found")
}
func (f downloadableSource) ResolveDownload(context.Context, string, string) (DownloadPlan, error) {
	return DownloadPlan{URLs: []string{"https://example/" + f.name}}, nil
}

// servableSource is hosted: it lists and details models but has no bytes to
// hand over, so it deliberately does not implement Downloadable.
type servableSource struct {
	name  string
	knows []string
}

func (f servableSource) Name() string { return f.name }
func (f servableSource) List(context.Context, ListOptions) ([]Model, error) {
	out := make([]Model, 0, len(f.knows))
	for _, id := range f.knows {
		out = append(out, Model{Source: f.name, ID: id})
	}
	return out, nil
}
func (f servableSource) Detail(_ context.Context, id string) (Detail, error) {
	for _, k := range f.knows {
		if k == id {
			return Detail{Source: f.name, ID: id}, nil
		}
	}
	return Detail{}, errors.New("not found")
}

// failingSource errors on every call — stands in for a source that is down.
type failingSource struct{ name string }

func (f failingSource) Name() string { return f.name }
func (f failingSource) List(context.Context, ListOptions) ([]Model, error) {
	return nil, errors.New("upstream unavailable")
}
func (f failingSource) Detail(context.Context, string) (Detail, error) {
	return Detail{}, errors.New("upstream unavailable")
}

func TestRegistry_LookupByName(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "huggingface"})
	r.Register(servableSource{name: "deepinfra"})

	for _, want := range []string{"huggingface", "deepinfra"} {
		got, ok := r.Lookup(want)
		if !ok {
			t.Fatalf("Lookup(%q): not found", want)
		}
		if got.Name() != want {
			t.Errorf("Lookup(%q) returned source %q", want, got.Name())
		}
	}
	if _, ok := r.Lookup("bogus"); ok {
		t.Error("Lookup of an unregistered name should report not-found, not fall back to another source")
	}
}

// The registry has no notion of an active source: registering several leaves
// all of them reachable, which is what lets browse show every source at once.
func TestRegistry_AllReturnsEverySourceSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "ollama"})
	r.Register(servableSource{name: "deepinfra"})
	r.Register(downloadableSource{name: "huggingface"})

	got := r.All()
	if len(got) != 3 {
		t.Fatalf("All() returned %d sources, want 3", len(got))
	}
	want := []string{"deepinfra", "huggingface", "ollama"}
	for i, w := range want {
		if got[i].Name() != w {
			t.Errorf("All()[%d] = %q, want %q (name order)", i, got[i].Name(), w)
		}
	}
}

func TestRegistry_Available(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "ollama"})
	r.Register(downloadableSource{name: "huggingface"})
	got := r.Available()
	if len(got) != 2 || got[0] != "huggingface" || got[1] != "ollama" {
		t.Errorf("Available() = %v, want [huggingface ollama] sorted", got)
	}
}

// A qualified ref resolves to the source it names — not to whichever source
// happens to be first or most recently registered.
func TestRegistry_ResolveQualifiedUsesNamedSource(t *testing.T) {
	r := NewRegistry()
	// Both sources know the same id, so a wrong resolution would still
	// "succeed" — only the returned source name reveals the mistake.
	r.Register(downloadableSource{name: "huggingface", knows: []string{"shared-id"}})
	r.Register(downloadableSource{name: "ollama", knows: []string{"shared-id"}})

	src, ref, err := r.Resolve(context.Background(), Ref{Source: "ollama", ID: "shared-id"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.Name() != "ollama" {
		t.Errorf("resolved to source %q, want ollama (the source named in the ref)", src.Name())
	}
	if ref.Source != "ollama" || ref.ID != "shared-id" {
		t.Errorf("returned ref = %v, want {ollama shared-id}", ref)
	}
}

func TestRegistry_ResolveUnknownSourceErrors(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "huggingface", knows: []string{"m"}})

	_, _, err := r.Resolve(context.Background(), Ref{Source: "bogus", ID: "m"})
	if err == nil {
		t.Fatal("resolving a ref naming an unregistered source must error, not fall back to another source")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error %q should name the unknown source", err)
	}
}

// An unqualified ref (older client) is resolved by probing the sources for
// the id, rather than assuming one.
func TestRegistry_ResolveUnqualifiedProbesSources(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "huggingface", knows: []string{"hf-only"}})
	r.Register(downloadableSource{name: "ollama", knows: []string{"ollama-only"}})

	src, ref, err := r.Resolve(context.Background(), Ref{ID: "ollama-only"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.Name() != "ollama" {
		t.Errorf("probe resolved to %q, want ollama (the source that knows the id)", src.Name())
	}
	// The caller gets back a qualified ref it can use from then on.
	if ref.Source != "ollama" {
		t.Errorf("returned ref.Source = %q, want it qualified with ollama", ref.Source)
	}
}

func TestRegistry_ResolveUnqualifiedUnknownIDErrors(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "huggingface", knows: []string{"hf-only"}})

	_, _, err := r.Resolve(context.Background(), Ref{ID: "nobody-has-this"})
	if err == nil {
		t.Fatal("an id no source recognizes must error rather than resolving arbitrarily")
	}
}

func TestRegistry_ResolveEmptyIDErrors(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "huggingface"})
	if _, _, err := r.Resolve(context.Background(), Ref{}); err == nil {
		t.Fatal("an empty model id must error")
	}
}

// The download path must refuse a hosted model: there are no bytes to fetch.
func TestRegistry_ResolveDownloadableRejectsServableSource(t *testing.T) {
	r := NewRegistry()
	r.Register(servableSource{name: "deepinfra", knows: []string{"glm-4.6"}})

	_, _, err := r.ResolveDownloadable(context.Background(), Ref{Source: "deepinfra", ID: "glm-4.6"})
	if err == nil {
		t.Fatal("ResolveDownloadable must refuse a servable-only source")
	}
	if !strings.Contains(err.Error(), "deepinfra") {
		t.Errorf("error %q should name the offending source", err)
	}
	// The same ref still resolves for read-only use (browse, RAM estimate).
	if _, _, err := r.Resolve(context.Background(), Ref{Source: "deepinfra", ID: "glm-4.6"}); err != nil {
		t.Errorf("Resolve should still serve a servable source: %v", err)
	}
}

func TestRegistry_ResolveDownloadableAllowsDownloadableSource(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "ollama", knows: []string{"qwen:7b"}})

	dl, ref, err := r.ResolveDownloadable(context.Background(), Ref{Source: "ollama", ID: "qwen:7b"})
	if err != nil {
		t.Fatalf("ResolveDownloadable: %v", err)
	}
	if ref.ID != "qwen:7b" {
		t.Errorf("ref.ID = %q, want qwen:7b", ref.ID)
	}
	if _, err := dl.ResolveDownload(context.Background(), ref.ID, ""); err != nil {
		t.Errorf("ResolveDownload on the returned Downloadable: %v", err)
	}
}

func TestRegistry_LookupDownloadable(t *testing.T) {
	r := NewRegistry()
	r.Register(downloadableSource{name: "ollama"})
	r.Register(servableSource{name: "deepinfra"})

	if _, ok := r.LookupDownloadable("ollama"); !ok {
		t.Error("ollama is downloadable and should be returned")
	}
	if _, ok := r.LookupDownloadable("deepinfra"); ok {
		t.Error("deepinfra serves rather than downloads; LookupDownloadable must not return it")
	}
	if _, ok := r.LookupDownloadable("bogus"); ok {
		t.Error("unregistered name must not resolve")
	}
}

// A source that is down is skipped during probing rather than aborting the
// resolve — the same tolerance browse relies on.
func TestRegistry_ResolveSkipsFailingSource(t *testing.T) {
	r := NewRegistry()
	r.Register(failingSource{name: "aaa-down"}) // sorts first, so it is probed first
	r.Register(downloadableSource{name: "ollama", knows: []string{"qwen:7b"}})

	src, _, err := r.Resolve(context.Background(), Ref{ID: "qwen:7b"})
	if err != nil {
		t.Fatalf("Resolve should skip the failing source and find the healthy one: %v", err)
	}
	if src.Name() != "ollama" {
		t.Errorf("resolved to %q, want ollama", src.Name())
	}
}

func TestRef_String(t *testing.T) {
	if got := (Ref{Source: "ollama", ID: "qwen:7b"}).String(); got != "ollama/qwen:7b" {
		t.Errorf("String() = %q, want ollama/qwen:7b", got)
	}
	if got := (Ref{ID: "qwen:7b"}).String(); got != "qwen:7b" {
		t.Errorf("unqualified String() = %q, want bare id", got)
	}
	if !(Ref{Source: "ollama", ID: "x"}).Qualified() {
		t.Error("a ref with both halves is qualified")
	}
	if (Ref{ID: "x"}).Qualified() {
		t.Error("a ref without a source is not qualified")
	}
}
