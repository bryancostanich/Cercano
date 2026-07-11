package ollamacatalog

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/catalog"
)

// fakeSource stubs ollamaSource so the adapter's mapping logic is exercised
// without a live Ollama registry.
type fakeSource struct {
	models      []Model
	tags        map[string][]string
	resolveURL  string
	resolveSize int64
	resolveErr  error
	resolvedRef string // captured for assertion
}

func (f *fakeSource) Models() []Model { return f.models }
func (f *fakeSource) ListTags(name string) ([]string, error) {
	return f.tags[name], nil
}
func (f *fakeSource) Resolve(_ context.Context, ref string) (string, int64, error) {
	f.resolvedRef = ref
	return f.resolveURL, f.resolveSize, f.resolveErr
}

func stubArch(arch string, err error) func(context.Context, string) (string, error) {
	return func(context.Context, string) (string, error) { return arch, err }
}

func TestOllamaBackend_ListMapsFamilies(t *testing.T) {
	src := &fakeSource{models: []Model{{Name: "qwen2.5-coder"}, {Name: "llama3.2"}, {Name: "phi4"}}}
	b := &Backend{src: src, archReader: stubArch("", nil)}

	got, err := b.List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d models, want 3", len(got))
	}
	if got[0].Backend != "ollama" || got[0].ID != "qwen2.5-coder" {
		t.Errorf("model0 = %+v, want ollama/qwen2.5-coder", got[0])
	}

	// Query narrows by substring on the family name.
	q, _ := b.List(context.Background(), catalog.ListOptions{Query: "qwen"})
	if len(q) != 1 || q[0].ID != "qwen2.5-coder" {
		t.Errorf("query result = %+v, want just qwen2.5-coder", q)
	}

	// Limit bounds the result.
	l, _ := b.List(context.Background(), catalog.ListOptions{Limit: 2})
	if len(l) != 2 {
		t.Errorf("limit result len = %d, want 2", len(l))
	}
}

func TestOllamaBackend_DetailTagsAndArch(t *testing.T) {
	src := &fakeSource{
		tags:        map[string][]string{"qwen2.5-coder": {"7b", "1.5b"}},
		resolveURL:  "https://registry.ollama.ai/v2/library/qwen2.5-coder/blobs/sha256:abc",
		resolveSize: 100,
	}
	b := &Backend{src: src, archReader: stubArch("qwen2", nil)}

	d, err := b.Detail(context.Background(), "qwen2.5-coder")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d.Architecture != "qwen2" {
		t.Errorf("arch = %q, want qwen2 (from the blob header read)", d.Architecture)
	}
	if len(d.Files) != 2 || d.Files[0].Name != "7b" || d.Files[1].Name != "1.5b" {
		t.Errorf("files = %+v, want tags [7b 1.5b]", d.Files)
	}
	// Arch is read from the first tag's blob.
	if src.resolvedRef != "qwen2.5-coder:7b" {
		t.Errorf("resolved ref = %q, want qwen2.5-coder:7b", src.resolvedRef)
	}
}

func TestOllamaBackend_DetailArchReadFailureLeavesEmpty(t *testing.T) {
	src := &fakeSource{
		tags:       map[string][]string{"fam": {"latest"}},
		resolveURL: "https://x/blob",
	}
	b := &Backend{src: src, archReader: stubArch("", errors.New("range read failed"))}

	d, err := b.Detail(context.Background(), "fam")
	if err != nil {
		t.Fatalf("Detail should not fail on a best-effort arch read: %v", err)
	}
	if d.Architecture != "" {
		t.Errorf("arch = %q, want empty on read failure (gate treats unknown as unsupported)", d.Architecture)
	}
	if len(d.Files) != 1 {
		t.Errorf("files = %+v, want the one tag", d.Files)
	}
}

func TestOllamaBackend_ResolveDownload(t *testing.T) {
	src := &fakeSource{
		resolveURL:  "https://registry.ollama.ai/v2/library/qwen2.5-coder/blobs/sha256:deadbeef",
		resolveSize: 4683073536,
	}
	b := &Backend{src: src, archReader: stubArch("", nil)}

	plan, err := b.ResolveDownload(context.Background(), "qwen2.5-coder", "7b")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if src.resolvedRef != "qwen2.5-coder:7b" {
		t.Errorf("resolved ref = %q, want qwen2.5-coder:7b", src.resolvedRef)
	}
	if len(plan.URLs) != 1 || plan.URLs[0] != src.resolveURL {
		t.Errorf("plan URLs = %v, want [%s]", plan.URLs, src.resolveURL)
	}
	if plan.TotalBytes != 4683073536 {
		t.Errorf("total = %d, want 4683073536", plan.TotalBytes)
	}
	if plan.PrimaryFile != "qwen2.5-coder-7b.gguf" {
		t.Errorf("primary = %q, want qwen2.5-coder-7b.gguf", plan.PrimaryFile)
	}
}
