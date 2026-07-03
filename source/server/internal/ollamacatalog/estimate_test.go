package ollamacatalog

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// buildTestGGUF assembles a minimal GGUF header with qwen-style
// architecture keys followed by filler so the blob is bigger than the
// requested window — exercising the bounded-read path.
func buildTestGGUF() []byte {
	var kvs bytes.Buffer
	writeStr := func(w *bytes.Buffer, s string) {
		binary.Write(w, binary.LittleEndian, uint64(len(s)))
		w.WriteString(s)
	}
	addStr := func(key, val string) {
		writeStr(&kvs, key)
		binary.Write(&kvs, binary.LittleEndian, uint32(8)) // string
		writeStr(&kvs, val)
	}
	addU32 := func(key string, val uint32) {
		writeStr(&kvs, key)
		binary.Write(&kvs, binary.LittleEndian, uint32(4)) // uint32
		binary.Write(&kvs, binary.LittleEndian, val)
	}
	addStr("general.architecture", "qwen2")
	addU32("qwen2.block_count", 28)
	addU32("qwen2.context_length", 32768)
	addU32("qwen2.embedding_length", 3584)
	addU32("qwen2.attention.head_count", 28)
	addU32("qwen2.attention.head_count_kv", 4)

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint32(0x46554747)) // magic
	binary.Write(&out, binary.LittleEndian, uint32(3))          // version
	binary.Write(&out, binary.LittleEndian, uint64(0))          // tensors
	binary.Write(&out, binary.LittleEndian, uint64(6))          // kv count
	out.Write(kvs.Bytes())
	out.Write(bytes.Repeat([]byte{0xAB}, 512*1024)) // filler past the window
	return out.Bytes()
}

// estimateTestServer serves an OCI manifest and a Range-aware blob.
// Counters let tests assert exactly which fetches happened.
type estimateTestServer struct {
	srv           *httptest.Server
	blob          []byte
	digest        string
	blobSize      int64
	manifestGets  atomic.Int64
	blobGets      atomic.Int64
	servedDigest  atomic.Value // string — lets tests "repoint the tag"
}

func newEstimateTestServer(t *testing.T) *estimateTestServer {
	t.Helper()
	e := &estimateTestServer{blob: buildTestGGUF()}
	e.digest = "sha256:aaaa000000000000000000000000000000000000000000000000000000000000"
	e.servedDigest.Store(e.digest)
	e.blobSize = int64(len(e.blob))
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/testmodel/manifests/", func(w http.ResponseWriter, r *http.Request) {
		e.manifestGets.Add(1)
		digest := e.servedDigest.Load().(string)
		manifest := map[string]any{
			"schemaVersion": 2,
			"layers": []map[string]any{
				{"mediaType": "application/vnd.ollama.image.model", "digest": digest, "size": e.blobSize},
			},
		}
		json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc("/v2/library/testmodel/blobs/", func(w http.ResponseWriter, r *http.Request) {
		e.blobGets.Add(1)
		rangeHdr := r.Header.Get("Range")
		if rangeHdr == "" {
			w.Write(e.blob)
			return
		}
		// Parse "bytes=0-N".
		spec := strings.TrimPrefix(rangeHdr, "bytes=")
		parts := strings.SplitN(spec, "-", 2)
		start, _ := strconv.ParseInt(parts[0], 10, 64)
		end, _ := strconv.ParseInt(parts[1], 10, 64)
		if end >= int64(len(e.blob)) {
			end = int64(len(e.blob)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(e.blob)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(e.blob[start : end+1])
	})
	mux.HandleFunc("/library", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><a href="/library/testmodel">testmodel</a></html>`)
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func newEstimateManager(t *testing.T, e *estimateTestServer) *Manager {
	t.Helper()
	f := &Fetcher{RegistryURL: e.srv.URL, BaseURL: e.srv.URL}
	return NewManager(f, filepath.Join(t.TempDir(), "cache.json"))
}

func TestResolveEstimate_FullResolve(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	est, err := m.ResolveEstimate(context.Background(), "testmodel:7b")
	if err != nil {
		t.Fatalf("ResolveEstimate: %v", err)
	}
	if est.WeightsBytes != e.blobSize {
		t.Errorf("WeightsBytes = %d, want %d", est.WeightsBytes, e.blobSize)
	}
	// 28 x 4 x (128+128) x 2 — same math as the gguf package tests.
	if est.KVBytesPerToken != 57344 {
		t.Errorf("KVBytesPerToken = %d, want 57344", est.KVBytesPerToken)
	}
	if est.MaxContextTokens != 32768 {
		t.Errorf("MaxContextTokens = %d, want 32768", est.MaxContextTokens)
	}
	if est.Architecture != "qwen2" {
		t.Errorf("Architecture = %q", est.Architecture)
	}
	if e.manifestGets.Load() != 1 || e.blobGets.Load() != 1 {
		t.Errorf("fetches = %d manifest / %d blob, want 1/1", e.manifestGets.Load(), e.blobGets.Load())
	}
}

func TestResolveEstimate_FreshCacheHitCostsNoNetwork(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	if _, err := m.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, err := m.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if e.manifestGets.Load() != 1 || e.blobGets.Load() != 1 {
		t.Errorf("fetches after cache hit = %d manifest / %d blob, want 1/1", e.manifestGets.Load(), e.blobGets.Load())
	}
}

func TestResolveEstimate_TTLLapsedSameDigestSkipsBlobFetch(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	if _, err := m.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	m.SetTTL(time.Nanosecond) // force staleness
	time.Sleep(time.Millisecond)
	est, err := m.ResolveEstimate(context.Background(), "testmodel:7b")
	if err != nil {
		t.Fatalf("revalidating resolve: %v", err)
	}
	if est.KVBytesPerToken != 57344 {
		t.Errorf("KVBytesPerToken = %d after revalidation", est.KVBytesPerToken)
	}
	if e.manifestGets.Load() != 2 {
		t.Errorf("manifest fetches = %d, want 2 (revalidation)", e.manifestGets.Load())
	}
	if e.blobGets.Load() != 1 {
		t.Errorf("blob fetches = %d, want 1 (digest unchanged)", e.blobGets.Load())
	}
}

func TestResolveEstimate_DigestChangeRefetchesWindow(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	if _, err := m.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	m.SetTTL(time.Nanosecond)
	time.Sleep(time.Millisecond)
	e.servedDigest.Store("sha256:bbbb000000000000000000000000000000000000000000000000000000000000")
	if _, err := m.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("post-repoint resolve: %v", err)
	}
	if e.blobGets.Load() != 2 {
		t.Errorf("blob fetches = %d, want 2 (digest moved)", e.blobGets.Load())
	}
}

func TestResolveEstimate_InvalidRef(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	if _, err := m.ResolveEstimate(context.Background(), "tagless"); err == nil {
		t.Fatal("expected error for tagless ref")
	}
}

func TestResolveEstimate_PersistsAcrossManagers(t *testing.T) {
	e := newEstimateTestServer(t)
	f := &Fetcher{RegistryURL: e.srv.URL, BaseURL: e.srv.URL}
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	m1 := NewManager(f, cachePath)
	if _, err := m1.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	m2 := NewManager(f, cachePath)
	if err := m2.LoadCache(); err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if _, err := m2.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("resolve on second manager: %v", err)
	}
	if e.manifestGets.Load() != 1 || e.blobGets.Load() != 1 {
		t.Errorf("fetches = %d manifest / %d blob, want 1/1 (disk cache should serve)", e.manifestGets.Load(), e.blobGets.Load())
	}
}

func TestRefresh_PreservesEstimates(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	if _, err := m.ResolveEstimate(context.Background(), "testmodel:7b"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, ok := m.cachedEstimate("testmodel:7b", time.Now()); !ok {
		t.Error("estimate lost after catalog refresh")
	}
}
