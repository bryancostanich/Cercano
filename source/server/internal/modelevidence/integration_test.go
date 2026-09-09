package modelevidence_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"cercano/source/server/internal/catalog"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/deepinfracatalog"
	"cercano/source/server/internal/modelevidence"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/internal/requestassembly"
	"cercano/source/server/pkg/config"
)

// deepinfraFixture serves the recorded index shape over a local endpoint, so
// this exercises the real catalog source (filters, parsing, cache) rather than
// a hand-built stand-in.
func deepinfraFixture(t *testing.T) *catalog.Registry {
	t.Helper()
	raw, err := os.ReadFile("../deepinfracatalog/testdata/models_list.json")
	if err != nil {
		t.Fatal(err)
	}
	var check []map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("fixture is not the recorded index shape: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	reg := catalog.NewRegistry()
	reg.Register(deepinfracatalog.New(
		deepinfracatalog.WithIndexURL(srv.URL),
		deepinfracatalog.WithHTTPClient(srv.Client()),
	))
	return reg
}

func deepinfraProfile() config.CloudProfile {
	return config.CloudProfile{
		Name: "deepinfra", Provider: "deepinfra",
		Flavor:  cloudfactory.FlavorChatCompletions,
		BaseURL: "https://api.deepinfra.com/v1/openai",
	}
}

// Failure 1: a hosted model's published window reached no consumer, so every
// request was budgeted against the 128K default.
func TestOriginalFailure_ContextCapacityNowReachesBudgeting(t *testing.T) {
	res := modelevidence.New(deepinfraFixture(t))
	prof := deepinfraProfile()
	const model = "Qwen/Qwen3.8-2.4T-A95B"

	ev := res.Resolve(context.Background(), modelevidence.IdentityFor(prof, model))
	if ev.ContextWindow <= 0 {
		t.Fatal("no capacity discovered from the index")
	}
	if ev.ContextWindow == 128_000 {
		t.Fatal("discovered capacity is indistinguishable from the old default")
	}

	// requestassembly is the budgeting consumer. The runner fills these Target
	// fields from the same evidence (see runner's knownContextWindowFor tests);
	// this asserts the value survives into the budget rather than being
	// overridden by the model-name table.
	window, known := requestassembly.WindowForTarget(requestassembly.Target{
		Model:              model,
		ContextWindow:      ev.ContextWindow,
		ContextWindowKnown: true,
	})
	if window != ev.ContextWindow || !known {
		t.Fatalf("budget window = %d/%v, want discovered %d/true", window, known, ev.ContextWindow)
	}

	// Without the evidence, the model-name table answers from family matching
	// alone. For this model it confidently returns 131072 while the provider
	// publishes 262144 — the exact discrepancy that motivated this fix, and
	// worse than a plain default because it is reported as "known".
	before, beforeKnown := requestassembly.WindowForTarget(requestassembly.Target{Model: model})
	if before == ev.ContextWindow {
		t.Fatal("name-table and published capacity agree; this fixture no longer demonstrates the failure")
	}
	t.Logf("name-table said %d (known=%v); provider publishes %d", before, beforeKnown, ev.ContextWindow)
}

// Failure 2: an image could be sent to a cloud model with no confirmed image
// support, because the transport flag was treated as model capability.
func TestOriginalFailure_UnconfirmedModelGetsNoVisionClient(t *testing.T) {
	res := modelevidence.New(deepinfraFixture(t))
	prof := deepinfraProfile()
	confirmed := func(model string) bool {
		return res.Resolve(context.Background(), modelevidence.IdentityFor(prof, model)).Vision == modelmetadata.VisionSupported
	}

	// Tools-capable, live, but the index publishes no image signal for it.
	textOnly := prof
	textOnly.Model = "Qwen/Qwen3.8-2.4T-A95B"
	prov, err := cloudfactory.BuildCloudProvider(textOnly, "key", cloudfactory.Options{ModelSupportsVision: confirmed})
	if err != nil {
		t.Fatal(err)
	}
	if prov.Capabilities().SupportsVision {
		t.Fatal("model without published image support advertised vision")
	}

	// A model the index does tag multimodal keeps vision.
	multimodal := prof
	multimodal.Model = "anthropic/claude-sonnet-4-6"
	prov, err = cloudfactory.BuildCloudProvider(multimodal, "key", cloudfactory.Options{ModelSupportsVision: confirmed})
	if err != nil {
		t.Fatal(err)
	}
	if !prov.Capabilities().SupportsVision {
		t.Fatal("model with published image support lost vision")
	}
}

// The index is consulted through the source's cache, not re-fetched per
// question.
func TestEvidence_ReusesCatalogCache(t *testing.T) {
	raw, err := os.ReadFile("../deepinfracatalog/testdata/models_list.json")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(raw)
	}))
	defer srv.Close()
	reg := catalog.NewRegistry()
	reg.Register(deepinfracatalog.New(
		deepinfracatalog.WithIndexURL(srv.URL),
		deepinfracatalog.WithHTTPClient(srv.Client()),
	))
	res := modelevidence.New(reg)
	prof := deepinfraProfile()
	for _, m := range []string{"Qwen/Qwen3.8-2.4T-A95B", "anthropic/claude-sonnet-4-6", "Qwen/Qwen3.8-2.4T-A95B"} {
		res.Resolve(context.Background(), modelevidence.IdentityFor(prof, m))
	}
	if hits > 1 {
		t.Fatalf("index fetched %d times; expected one cached fetch", hits)
	}
}
