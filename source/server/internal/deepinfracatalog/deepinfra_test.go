package deepinfracatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/catalog"
)

// fixtureServer serves the recorded /models/list payload and counts hits, so
// tests can assert on caching as well as content.
func fixtureServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "models_list.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newTestSource(t *testing.T, srv *httptest.Server, opts ...Option) *Source {
	t.Helper()
	all := append([]Option{WithIndexURL(srv.URL), WithHTTPClient(srv.Client())}, opts...)
	return New(all...)
}

// TestList_FiltersToUsableModels is the core filter assertion: of the 11
// recorded entries only the 4 that are text-generation, tool-capable, and
// live may surface.
func TestList_FiltersToUsableModels(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)

	got, err := s.List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := map[string]bool{
		"anthropic/claude-sonnet-4-6":              true,
		"Qwen/Qwen3.8-2.4T-A95B":                   true,
		"Qwen/Qwen3-235B-A22B-Instruct-2507":       true,
		"nvidia/NVIDIA-Nemotron-3-Super-120B-A12B": true,
	}
	if len(got) != len(want) {
		var ids []string
		for _, m := range got {
			ids = append(ids, m.ID)
		}
		t.Fatalf("List returned %d models, want %d: %v", len(got), len(want), ids)
	}
	for _, m := range got {
		if !want[m.ID] {
			t.Errorf("unexpected model in List: %q", m.ID)
		}
		if m.Source != SourceName {
			t.Errorf("%s: Source = %q, want %q", m.ID, m.Source, SourceName)
		}
		if m.Kind != catalog.KindTextGeneration {
			t.Errorf("%s: Kind = %q, want text-generation", m.ID, m.Kind)
		}
		if !m.SupportsTools {
			t.Errorf("%s: SupportsTools = false, want true", m.ID)
		}
		if m.Deprecated {
			t.Errorf("%s: Deprecated = true, should have been filtered out", m.ID)
		}
	}
}

// TestList_ExcludesEachIneligibleReason pins *why* each excluded entry is
// excluded, so a filter regression names the specific rule that broke rather
// than just reporting a count mismatch.
func TestList_ExcludesEachIneligibleReason(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)

	got, err := s.List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	present := map[string]bool{}
	for _, m := range got {
		present[m.ID] = true
	}

	for _, tc := range []struct{ id, reason string }{
		{"zai-org/GLM-4.5-Air", "deprecated"},
		{"nvidia/Nemotron-Content-Safety-3.5", "no tools tag"},
		{"run-diffusion/Juggernaut-Flux", "text-to-image"},
		{"thenlper/gte-base", "embeddings"},
		{"inworld-ai/realtime-tts-2", "text-to-speech"},
		{"mistralai/Voxtral-Small-24B-2507", "speech recognition"},
		{"Wan-AI/Wan2.7-R2V", "text-to-video"},
	} {
		if present[tc.id] {
			t.Errorf("%s should be excluded (%s) but appeared in List", tc.id, tc.reason)
		}
	}
}

// TestPricing_ExactConversion checks the cents/token -> micro-USD/Mtok math on
// real quoted values, including the IEEE-754 hazard: 0.0003 * 1e10 evaluates
// to 2999999.9999999995, which truncation would render as 2999999.
func TestPricing_ExactConversion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cents float64
		want  int64
	}{
		{"float hazard 0.0003", 0.0003, 3_000_000},
		{"float hazard 5e-06", 5e-06, 50_000},
		{"typical input 0.0002", 0.0002, 2_000_000},
		{"typical output 0.0006", 0.0006, 6_000_000},
		{"cheapest 1.9e-06", 1.9e-06, 19_000},
		{"priciest 0.001", 0.001, 10_000_000},
		{"zero", 0, 0},
		{"negative is rejected", -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := centsPerTokenToMicroUSDPerMillion(tc.cents); got != tc.want {
				t.Errorf("centsPerTokenToMicroUSDPerMillion(%v) = %d, want %d", tc.cents, got, tc.want)
			}
		})
	}
}

// TestList_CarriesPricing confirms price rides on the list row, which is the
// reason it lives on catalog.Model rather than only on Detail — the picker
// must be able to show cost without a Detail call per model.
func TestList_CarriesPricing(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)

	got, err := s.List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, m := range got {
		if m.PriceIn <= 0 || m.PriceOut <= 0 {
			t.Errorf("%s: PriceIn=%d PriceOut=%d, want both > 0", m.ID, m.PriceIn, m.PriceOut)
		}
		if m.ContextLength <= 0 {
			t.Errorf("%s: ContextLength = %d, want > 0", m.ID, m.ContextLength)
		}
		if m.Publisher == "" {
			t.Errorf("%s: Publisher empty, want org prefix", m.ID)
		}
	}
}

func TestPublisherOf(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"Qwen/Qwen3-235B", "Qwen"},
		{"anthropic/claude-sonnet-4-6", "anthropic"},
		{"nested/org/model", "nested"},
		{"bare-model", ""},
		{"/leading-slash", ""},
	} {
		if got := publisherOf(tc.id); got != tc.want {
			t.Errorf("publisherOf(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestDetail_ReturnsServedVariant checks the single-variant shape a hosted
// model takes: quantization and price, but no on-disk size.
func TestDetail_ReturnsServedVariant(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)

	d, err := s.Detail(context.Background(), "Qwen/Qwen3.8-2.4T-A95B")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d.Source != SourceName || d.ID != "Qwen/Qwen3.8-2.4T-A95B" {
		t.Errorf("Detail identity = %q/%q", d.Source, d.ID)
	}
	if len(d.Variants) != 1 {
		t.Fatalf("Variants = %d, want 1 served flavor", len(d.Variants))
	}
	v := d.Variants[0]
	if v.Quantization != "fp4" {
		t.Errorf("Quantization = %q, want fp4", v.Quantization)
	}
	if v.SizeBytes != 0 {
		t.Errorf("SizeBytes = %d, want 0 — a served model has no local file", v.SizeBytes)
	}
	if v.PriceIn <= 0 || v.PriceOut <= 0 {
		t.Errorf("variant pricing not mapped: in=%d out=%d", v.PriceIn, v.PriceOut)
	}
}

// TestDetail_ExplainsRetiredModel is the reason Detail searches the unfiltered
// index while List does not. A model pinned in config can be retired upstream;
// answering "unknown model" would hide the actual problem, so Detail must
// still resolve it and report the successor.
func TestDetail_ExplainsRetiredModel(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)

	d, err := s.Detail(context.Background(), "zai-org/GLM-4.5-Air")
	if err != nil {
		t.Fatalf("Detail on retired model should succeed, got: %v", err)
	}
	if !d.Deprecated {
		t.Error("Deprecated = false, want true")
	}
	if d.ReplacedBy == "" {
		t.Error("ReplacedBy empty; a retirement should name its successor")
	}
}

func TestDetail_UnknownModel(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)

	if _, err := s.Detail(context.Background(), "nope/not-a-model"); err == nil {
		t.Fatal("Detail on unknown id returned nil error")
	}
}

func TestList_QueryAndLimit(t *testing.T) {
	srv, _ := fixtureServer(t)
	s := newTestSource(t, srv)
	ctx := context.Background()

	got, err := s.List(ctx, catalog.ListOptions{Query: "qwen"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("query \"qwen\" matched %d, want 2", len(got))
	}

	got, err = s.List(ctx, catalog.ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("Limit 1 returned %d", len(got))
	}
}

// TestNotDownloadable is the load-bearing negative assertion of this package.
// DeepInfra models are served, never fetched; the type must fail the
// Downloadable assertion so the registry's download path rejects it.
func TestNotDownloadable(t *testing.T) {
	var s catalog.Source = New()
	if _, ok := s.(catalog.Downloadable); ok {
		t.Fatal("Source must not implement catalog.Downloadable — its models are served, not downloaded")
	}
}

// TestCache_ServesWithinTTL asserts a browse inside the window costs no
// request.
func TestCache_ServesWithinTTL(t *testing.T) {
	srv, hits := fixtureServer(t)
	s := newTestSource(t, srv, WithTTL(time.Hour))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := s.List(ctx, catalog.ListOptions{}); err != nil {
			t.Fatalf("List %d: %v", i, err)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("fetched %d times, want 1 (TTL should absorb the rest)", n)
	}
}

// TestCache_RefetchesAfterTTL is the counterpart: the cache must expire, not
// pin the index forever.
func TestCache_RefetchesAfterTTL(t *testing.T) {
	srv, hits := fixtureServer(t)
	s := newTestSource(t, srv, WithTTL(time.Nanosecond))
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := s.List(ctx, catalog.ListOptions{}); err != nil {
			t.Fatalf("List %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
	if n := hits.Load(); n < 2 {
		t.Errorf("fetched %d times, want >= 2 (TTL expired between calls)", n)
	}
}

// TestCache_ServesStaleOnFailure is the degradation guarantee: once an index
// has been seen, a provider outage yields slightly-old data rather than an
// empty models page.
func TestCache_ServesStaleOnFailure(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "models_list.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	s := New(WithIndexURL(srv.URL), WithHTTPClient(srv.Client()), WithTTL(time.Nanosecond))
	ctx := context.Background()

	first, err := s.List(ctx, catalog.ListOptions{})
	if err != nil {
		t.Fatalf("warm fetch: %v", err)
	}

	fail.Store(true)
	time.Sleep(time.Millisecond) // ensure the TTL has lapsed

	second, err := s.List(ctx, catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List during outage should serve stale, got: %v", err)
	}
	if len(second) != len(first) {
		t.Errorf("stale serve returned %d models, want %d", len(second), len(first))
	}
}

// TestCache_ColdFailureIsAnError is the boundary of serve-stale: with nothing
// cached there is nothing to fall back to, and the error must surface so the
// server can omit the source.
func TestCache_ColdFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := New(WithIndexURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := s.List(context.Background(), catalog.ListOptions{}); err == nil {
		t.Fatal("cold-cache fetch failure returned nil error")
	}
}

// TestFetch_TimeoutIsBounded covers the gap that per-source failure handling
// does not: a source that is slow rather than dead. Without its own timeout a
// stalled fetch would hold the browse response until the caller's context
// expired.
func TestFetch_TimeoutIsBounded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	s := New(WithIndexURL(srv.URL), WithHTTPClient(srv.Client()), WithTimeout(50*time.Millisecond))

	start := time.Now()
	_, err := s.List(context.Background(), catalog.ListOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("stalled fetch returned nil error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v; the per-fetch timeout did not bound it", elapsed)
	}
}

// TestUnknownFieldsIgnored guards the defensive-parsing claim: /models/list is
// unversioned, so an upstream field addition must not break decoding.
func TestUnknownFieldsIgnored(t *testing.T) {
	payload := `[{"model_name":"acme/model","type":"text-generation","tags":["tools"],
	  "max_tokens":8192,"quantization":"fp8","deprecated":null,"replaced_by":"",
	  "pricing":{"type":"tokens","cents_per_input_token":0.0002,"cents_per_output_token":0.0006},
	  "brand_new_upstream_field":{"nested":[1,2,3]},"another":"value"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	s := New(WithIndexURL(srv.URL), WithHTTPClient(srv.Client()))
	got, err := s.List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatalf("List with unknown fields: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1", len(got))
	}
	if got[0].PriceIn != 2_000_000 || got[0].PriceOut != 6_000_000 {
		t.Errorf("pricing = in:%d out:%d", got[0].PriceIn, got[0].PriceOut)
	}
}

// TestNonTokenPricingNotCoerced: DeepInfra meters image and audio models per
// image or per second. Those units must not be silently mapped onto per-token
// fields, where they would read as a wildly wrong price.
func TestNonTokenPricingNotCoerced(t *testing.T) {
	m := wireModel{
		ModelName: "acme/img",
		Type:      "text-generation",
		Tags:      []string{"tools"},
		Pricing:   wirePrice{Type: "image_units"},
	}
	got := toModel(m)
	if got.PriceIn != 0 || got.PriceOut != 0 {
		t.Errorf("non-token pricing produced PriceIn=%d PriceOut=%d, want 0/0", got.PriceIn, got.PriceOut)
	}
}
