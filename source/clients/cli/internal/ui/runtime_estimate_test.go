package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/server/pkg/agentclient"
)

func TestCompactFitAnnot(t *testing.T) {
	const gb = int64(1) << 30
	sysRAM := 32 * gb          // usable ≈ 24 GB (0.75)
	cases := []struct {
		name    string
		weights int64
		want    string
	}{
		{"unknown weights", 0, ""},
		{"unknown ram via zero", -1, ""},
		{"small model fits", 4 * gb, "✓ fits"},           // fixed ~4.7GB « 24GB
		{"large model tight", 18 * gb, "△ tight"},        // fixed ~18.9GB > 80% of 24GB
		{"too-large won't fit", 30 * gb, "✗ won't fit"},  // fixed ~31.5GB > 24GB
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ram := sysRAM
			if c.weights == -1 { // exercise the zero-RAM guard
				c.weights, ram = 4*gb, 0
			}
			if got := compactFitAnnot(c.weights, ram); got != c.want {
				t.Errorf("compactFitAnnot(%d, %d) = %q; want %q", c.weights, ram, got, c.want)
			}
		})
	}
}

// qwenEstimate mirrors qwen2.5-coder:7b's real numbers: 4.68 GB
// weights, 57344 KV bytes/token, 32k max context.
func qwenEstimate(sysRAM int64) agentclient.ModelRAMEstimate {
	return agentclient.ModelRAMEstimate{
		WeightsBytes:     4683074048,
		KVBytesPerToken:  57344,
		MaxContextTokens: 32768,
		Architecture:     "qwen2",
		SystemRAMBytes:   sysRAM,
	}
}

func TestEstimateKey_RoutesLocalRefAndUnestimatable(t *testing.T) {
	local := agentclient.RuntimeModel{ID: "llama_server:m", Runtime: "llama_server", Path: "/tmp/m.gguf"}
	if got := estimateKey(local); got != "local:llama_server:llama_server:m" {
		t.Errorf("local key = %q", got)
	}
	downloaded := agentclient.RuntimeModel{ID: "x", Runtime: "llama_server", DownloadState: "downloaded", CatalogID: "x:7b"}
	if got := estimateKey(downloaded); !strings.HasPrefix(got, "local:") {
		t.Errorf("downloaded model should use the local path, got %q", got)
	}
	online := agentclient.RuntimeModel{ID: "llama_server:online:qwen2.5-coder", CatalogID: "qwen2.5-coder"}
	if got := estimateKey(online); got != "ref:qwen2.5-coder" {
		t.Errorf("online key = %q", got)
	}
	hardcoded := agentclient.RuntimeModel{ID: "llama_server:catalog:something", DownloadState: "not_downloaded"}
	if got := estimateKey(hardcoded); got != "" {
		t.Errorf("HF-URL catalog entry should be unestimatable, got %q", got)
	}
}

func TestEstimateContextPoints(t *testing.T) {
	if got := estimateContextPoints(131072); len(got) != 3 || got[2] != 131072 {
		t.Errorf("131k points = %v", got)
	}
	if got := estimateContextPoints(32768); len(got) != 2 || got[1] != 32768 {
		t.Errorf("32k points = %v", got)
	}
	if got := estimateContextPoints(4096); len(got) != 1 || got[0] != 4096 {
		t.Errorf("4k points = %v", got)
	}
	if got := estimateContextPoints(0); got != nil {
		t.Errorf("0 points = %v, want nil", got)
	}
}

func TestEstimateMemoryLine_Shape(t *testing.T) {
	line := estimateMemoryLine(qwenEstimate(0))
	if line == "" {
		t.Fatal("expected a memory line")
	}
	for _, want := range []string{"@8k", "@32k (max)", "~"} {
		if !strings.Contains(line, want) {
			t.Errorf("memory line %q missing %q", line, want)
		}
	}
}

func TestEstimateFitLine_Verdicts(t *testing.T) {
	// 64 GB machine: usable 48 GB; total@32k ≈ 4.68G + overhead(~0.75G)
	// + 1.75G ≈ 7.2G — comfortably full-context.
	if line := estimateFitLine(qwenEstimate(64 << 30)); !strings.HasPrefix(line, "✓ fits — full") {
		t.Errorf("64GB verdict = %q", line)
	}
	// 8 GB machine: usable 6 GB; fixed ≈ 5.4G, min-context total ≈
	// 5.66G fits, full 32k (≈7.2G) does not — partial fit.
	if line := estimateFitLine(qwenEstimate(8 << 30)); !strings.HasPrefix(line, "△ fits up to") {
		t.Errorf("8GB verdict = %q", line)
	}
	// 4 GB machine: usable 3 GB < weights alone — won't fit.
	if line := estimateFitLine(qwenEstimate(4 << 30)); !strings.HasPrefix(line, "✗ won't fit") {
		t.Errorf("4GB verdict = %q", line)
	}
	// Unknown system RAM: no verdict rather than a wrong one.
	if line := estimateFitLine(qwenEstimate(0)); line != "" {
		t.Errorf("unknown-RAM verdict = %q, want empty", line)
	}
}

func TestCatalogDetailLines_EstimateStates(t *testing.T) {
	m := New(nil, false)
	model := agentclient.RuntimeModel{ID: "llama_server:online:qwen2.5-coder", DisplayName: "Qwen2.5 Coder", CatalogID: "qwen2.5-coder"}

	pending := strings.Join(catalogDetailLines(model, nil, true, 60, m.styles), "\n")
	if !strings.Contains(ansi.Strip(pending), "estimating...") {
		t.Errorf("pending details missing estimating marker:\n%s", pending)
	}

	failed := agentclient.ModelRAMEstimate{Err: errors.New("boom")}
	failedOut := strings.Join(catalogDetailLines(model, &failed, false, 60, m.styles), "\n")
	if !strings.Contains(ansi.Strip(failedOut), "estimate unavailable") {
		t.Errorf("failed details missing unavailable marker:\n%s", failedOut)
	}

	est := qwenEstimate(64 << 30)
	okOut := ansi.Strip(strings.Join(catalogDetailLines(model, &est, false, 120, m.styles), "\n"))
	if !strings.Contains(okOut, "memory") || !strings.Contains(okOut, "@8k") {
		t.Errorf("details missing memory line:\n%s", okOut)
	}
	if !strings.Contains(okOut, "fit") || !strings.Contains(okOut, "✓ fits") {
		t.Errorf("details missing fit verdict:\n%s", okOut)
	}
	// The online entry has no SizeBytes — the estimate's weights size
	// should back-fill the size line.
	if !strings.Contains(okOut, "4.4 GB") {
		t.Errorf("details missing back-filled size:\n%s", okOut)
	}
}

func TestMaybeFetchEstimate_DedupAndPending(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Catalog: agentclient.RuntimeModelCatalog{
			Models: []agentclient.RuntimeModel{
				{ID: "llama_server:online:qwen2.5-coder", CatalogID: "qwen2.5-coder", DownloadState: "not_downloaded"},
			},
		},
	})
	if cmd := d.maybeFetchEstimate(); cmd == nil {
		t.Fatal("first call should return a fetch command")
	}
	if !d.estimatePending["ref:qwen2.5-coder"] {
		t.Fatal("pending flag not set")
	}
	if cmd := d.maybeFetchEstimate(); cmd != nil {
		t.Fatal("second call should dedup while in flight")
	}
	// Delivery clears pending, caches, and doesn't refetch.
	d.applyEstimate(runtimeEstimateMsg{key: "ref:qwen2.5-coder", est: qwenEstimate(1)})
	if d.estimatePending["ref:qwen2.5-coder"] {
		t.Fatal("pending flag not cleared on delivery")
	}
	if cmd := d.maybeFetchEstimate(); cmd != nil {
		t.Fatal("cached estimate should suppress refetch")
	}
}

func TestMaybeFetchEstimate_NilForUnestimatable(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Catalog: agentclient.RuntimeModelCatalog{
			Models: []agentclient.RuntimeModel{
				{ID: "llama_server:catalog:hf-entry", DownloadState: "not_downloaded"},
			},
		},
	})
	if cmd := d.maybeFetchEstimate(); cmd != nil {
		t.Fatal("HF-URL entry should not dispatch an estimate fetch")
	}
}

func TestSelectedEstimate_PrefersServerEmbeddedNumbers(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Catalog: agentclient.RuntimeModelCatalog{
			SystemRAMBytes: 64 << 30,
			Models: []agentclient.RuntimeModel{{
				ID:               "llama_server:online:qwen2.5-coder",
				CatalogID:        "qwen2.5-coder",
				DownloadState:    "not_downloaded",
				SizeBytes:        4683074048,
				KVBytesPerToken:  57344,
				MaxContextTokens: 32768,
			}},
		},
	})
	model := d.catalogModels()[0]
	est, pending := d.selectedEstimate(model)
	if pending {
		t.Fatal("embedded estimate should never be pending")
	}
	if est == nil || est.KVBytesPerToken != 57344 || est.SystemRAMBytes != 64<<30 {
		t.Fatalf("embedded estimate = %+v", est)
	}
	// Warmed entries must not dispatch a fetch.
	if cmd := d.maybeFetchEstimate(); cmd != nil {
		t.Fatal("warmed entry dispatched an estimate fetch")
	}
}

func TestSelectedEstimate_FallsBackToLazyFetchWhenUnwarmed(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Catalog: agentclient.RuntimeModelCatalog{
			Models: []agentclient.RuntimeModel{{
				ID:            "llama_server:online:newmodel",
				CatalogID:     "newmodel",
				DownloadState: "not_downloaded",
			}},
		},
	})
	if cmd := d.maybeFetchEstimate(); cmd == nil {
		t.Fatal("unwarmed entry should fall back to the lazy fetch")
	}
}

// A record mid-download has Path set to its destination while the file
// is still partial — the estimate must route through the registry
// resolver, not a doomed local header read.
func TestEstimateIsLocal_DownloadingRecordIsNotLocal(t *testing.T) {
	downloading := agentclient.RuntimeModel{
		Path:          "/tmp/dest.gguf",
		DownloadState: "downloading",
		CatalogID:     "nomic-embed-text:latest",
	}
	if estimateIsLocal(downloading) {
		t.Error("downloading record treated as local")
	}
	if key := estimateKey(downloading); key != "ref:nomic-embed-text:latest" {
		t.Errorf("key = %q, want the ref route", key)
	}
	done := agentclient.RuntimeModel{Path: "/tmp/dest.gguf", DownloadState: "downloaded"}
	if !estimateIsLocal(done) {
		t.Error("downloaded record should be local")
	}
	onDisk := agentclient.RuntimeModel{Path: "/tmp/found.gguf"}
	if !estimateIsLocal(onDisk) {
		t.Error("stateless on-disk record should be local")
	}
}
