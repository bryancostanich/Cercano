package ollamacatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// libraryFixture is a trimmed sample of the real ollama.com/library HTML
// — just enough to prove the regex picks up the model-family links but
// not so much that a schema tweak breaks the test.
const libraryFixture = `<!doctype html>
<html>
  <body>
    <a href="/library/qwen2.5-coder">qwen2.5-coder</a>
    <a href="/library/qwen2.5-coder">qwen2.5-coder</a>
    <a href="/library/llama3.2">llama3.2</a>
    <a href="/library/mistral">mistral</a>
    <a href="/blog">Blog</a>
    <a href="/library/gemma3">gemma3</a>
    <a href="/library/qwen2.5-coder:7b">qwen2.5-coder:7b</a>
  </body>
</html>`

const modelPageFixture = `<!doctype html>
<html>
  <body>
    <h2>Models</h2>
    <a href="/library/qwen2.5-coder:latest">latest</a>
    <a href="/library/qwen2.5-coder:0.5b">0.5b</a>
    <a href="/library/qwen2.5-coder:7b">7b</a>
    <a href="/library/qwen2.5-coder:7b">dup 7b</a>
    <a href="/library/qwen2.5-coder:32b">32b</a>
    <a href="/library/other-model:1b">wrong family</a>
  </body>
</html>`

func TestListModels_ExtractsFamiliesFromHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, libraryFixture)
	}))
	defer srv.Close()

	f := &Fetcher{BaseURL: srv.URL}
	models, err := f.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(models))
	for i, m := range models {
		names[i] = m.Name
	}
	want := []string{"gemma3", "llama3.2", "mistral", "qwen2.5-coder"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v (sorted, deduped, tag-links excluded)", names, want)
	}
}

func TestListTags_ExtractsTagsForFamilyOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/qwen2.5-coder" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, modelPageFixture)
	}))
	defer srv.Close()

	f := &Fetcher{BaseURL: srv.URL}
	tags, err := f.ListTags("qwen2.5-coder")
	if err != nil {
		t.Fatal(err)
	}
	// Sorted, deduped, and "other-model:1b" excluded because the family
	// name in that link doesn't match the requested one.
	want := []string{"0.5b", "32b", "7b", "latest"}
	if strings.Join(tags, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", tags, want)
	}
}

func TestListModels_ReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone fishing", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := &Fetcher{BaseURL: srv.URL}
	if _, err := f.ListModels(); err == nil {
		t.Fatal("expected error on non-200; got nil")
	}
}

// realManifestFixture matches the shape of a live manifest response
// captured from registry.ollama.ai during the design research —
// four layers, with the model layer's mediaType being the important
// signal. Everything else (config, template, system, license) is noise
// we discard.
const realManifestFixture = `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
  "config": {
    "mediaType": "application/vnd.docker.container.image.v1+json",
    "digest": "sha256:cfg",
    "size": 42
  },
  "layers": [
    {
      "mediaType": "application/vnd.ollama.image.model",
      "digest": "sha256:blob-model",
      "size": 4700000000
    },
    {
      "mediaType": "application/vnd.ollama.image.template",
      "digest": "sha256:blob-tmpl",
      "size": 1000
    },
    {
      "mediaType": "application/vnd.ollama.image.license",
      "digest": "sha256:blob-lic",
      "size": 500
    }
  ]
}`

func TestFetchManifestAndModelLayer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/qwen2.5-coder/manifests/7b" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		fmt.Fprint(w, realManifestFixture)
	}))
	defer srv.Close()

	f := &Fetcher{RegistryURL: srv.URL}
	m, err := f.FetchManifest("qwen2.5-coder", "7b")
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", m.SchemaVersion)
	}
	layer, err := m.ModelLayer()
	if err != nil {
		t.Fatal(err)
	}
	if layer.Digest != "sha256:blob-model" {
		t.Errorf("ModelLayer.Digest = %q, want sha256:blob-model", layer.Digest)
	}
	if layer.Size != 4700000000 {
		t.Errorf("ModelLayer.Size = %d, want 4700000000", layer.Size)
	}
}

func TestModelLayer_ReturnsErrorWhenAbsent(t *testing.T) {
	// A manifest with no application/vnd.ollama.image.model layer must
	// be a hard error — silently proceeding would end up downloading a
	// template file and trying to serve it as a GGUF.
	m := &Manifest{
		Layers: []ManifestLayer{
			{MediaType: "application/vnd.ollama.image.license", Digest: "sha256:lic"},
		},
	}
	if _, err := m.ModelLayer(); err == nil {
		t.Fatal("expected error when no model layer present; got nil")
	}
}

func TestDownloadBlob_ReturnsStreamAndSize(t *testing.T) {
	blobBytes := []byte("GGUF\x03\x00\x00\x00fake gguf body")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/qwen2.5-coder/blobs/sha256:blob-model" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(blobBytes)))
		w.Write(blobBytes)
	}))
	defer srv.Close()

	f := &Fetcher{RegistryURL: srv.URL}
	r, size, err := f.DownloadBlob("qwen2.5-coder", "sha256:blob-model")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if size != int64(len(blobBytes)) {
		t.Errorf("size = %d, want %d", size, len(blobBytes))
	}
	got, _ := io.ReadAll(r)
	if string(got) != string(blobBytes) {
		t.Errorf("stream mismatch:\ngot  %q\nwant %q", got, blobBytes)
	}
}

func TestDownloadBlobRange_ResumesFromOffset(t *testing.T) {
	blobBytes := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/foo/blobs/sha256:x" {
			http.NotFound(w, r)
			return
		}
		if rng := r.Header.Get("Range"); rng != "" {
			// Very simple Range parser: only supports "bytes=N-".
			var start int64
			fmt.Sscanf(rng, "bytes=%d-", &start)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(blobBytes)-1, len(blobBytes)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", int64(len(blobBytes))-start))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(blobBytes[start:])
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(blobBytes)))
		w.Write(blobBytes)
	}))
	defer srv.Close()

	f := &Fetcher{RegistryURL: srv.URL}
	r, remaining, err := f.DownloadBlobRange("foo", "sha256:x", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// The remaining count reported to the caller must reflect
	// "how many more bytes to expect" — not the original size — so
	// progress accounting works whether the caller resumed or started
	// fresh.
	if remaining != int64(len(blobBytes)-10) {
		t.Errorf("remaining = %d, want %d", remaining, len(blobBytes)-10)
	}
	got, _ := io.ReadAll(r)
	if string(got) != string(blobBytes[10:]) {
		t.Errorf("stream mismatch:\ngot  %q\nwant %q", got, blobBytes[10:])
	}
}

// Sanity: the JSON tags on Manifest match a real registry response.
func TestManifest_JSONTagsMatchLiveResponseShape(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal([]byte(realManifestFixture), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Layers) != 3 {
		t.Fatalf("Layers len = %d, want 3", len(m.Layers))
	}
	if m.Layers[0].MediaType != "application/vnd.ollama.image.model" {
		t.Errorf("Layers[0].MediaType = %q", m.Layers[0].MediaType)
	}
}
