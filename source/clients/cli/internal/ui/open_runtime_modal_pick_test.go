package ui

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// modelWithModal builds the minimal Model needed to drive
// handleOpenRuntimeModalKey and the modalModelsLoadedMsg route. agent stays
// nil — the tests assert on returned cmds without executing the RPC ones.
func modelWithModal(st agentclient.OpenRuntimeStatus) Model {
	p := theme.Cracker()
	return Model{
		palette:          p,
		styles:           theme.NewStyles(p),
		openRuntimeModal: newOpenRuntimeInstallModal(st),
	}
}

func ambiguousStatus() agentclient.OpenRuntimeStatus {
	return agentclient.OpenRuntimeStatus{
		Runtime: "llama_server",
		Missing: "model",
		Message: "llama-server detection: model: found 2 GGUF models; set llama_server.default_model to disambiguate",
	}
}

func twoGGUFs() []agentclient.RuntimeModel {
	return []agentclient.RuntimeModel{
		{ID: "llama_server:aaa", DisplayName: "qwen3-coder-30b-q4", Path: "/models/qwen3-coder-30b-q4.gguf", Quantization: "Q4_K_M", SizeBytes: 18 << 30},
		{ID: "llama_server:bbb", DisplayName: "llama3.3-70b-q3", Path: "/models/llama3.3-70b-q3.gguf", Quantization: "Q3_K_L", SizeBytes: 30 << 30},
	}
}

func TestOpenRuntimeModal_MissingModelOpensScanning(t *testing.T) {
	mo := newOpenRuntimeInstallModal(ambiguousStatus())
	if mo.state != runtimeModalScanningModels {
		t.Fatalf("state = %v, want runtimeModalScanningModels", mo.state)
	}
	if !modalOpensScanning(ambiguousStatus()) {
		t.Fatal("modalOpensScanning should be true for llama_server Missing==model")
	}
	if modalOpensScanning(agentclient.OpenRuntimeStatus{Runtime: "mistralrs", Missing: "model"}) {
		t.Fatal("modalOpensScanning should be false for mistralrs Missing==model")
	}
	if modalOpensScanning(agentclient.OpenRuntimeStatus{Runtime: "llama_server", Missing: "binary"}) {
		t.Fatal("modalOpensScanning should be false for Missing==binary")
	}
}

func TestOpenRuntimeModal_ModelsLoadedRoutesToPicker(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	next, _ := m.Update(modalModelsLoadedMsg{models: twoGGUFs()})
	nm := next.(Model)
	if nm.openRuntimeModal.state != runtimeModalPickModel {
		t.Fatalf("state = %v, want runtimeModalPickModel", nm.openRuntimeModal.state)
	}
	// The pick step is now the shared RowList overlay, seeded one row per
	// discovered GGUF (no clear row — a GGUF must be chosen to make
	// llama_server usable).
	if nm.openRuntimeModal.picker == nil || len(nm.openRuntimeModal.picker.Rows) != 2 {
		t.Fatalf("picker not initialized from the discovered models: %+v", nm.openRuntimeModal.picker)
	}
}

func TestOpenRuntimeModal_ZeroModelsRoutesToNeedsModel(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	next, _ := m.Update(modalModelsLoadedMsg{models: nil})
	nm := next.(Model)
	if nm.openRuntimeModal.state != runtimeModalNeedsModel {
		t.Fatalf("state = %v, want runtimeModalNeedsModel", nm.openRuntimeModal.state)
	}
}

func TestOpenRuntimeModal_FetchErrorRoutesToNeedsModel(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	next, _ := m.Update(modalModelsLoadedMsg{err: errors.New("rpc unavailable")})
	nm := next.(Model)
	if nm.openRuntimeModal.state != runtimeModalNeedsModel {
		t.Fatalf("state = %v, want runtimeModalNeedsModel", nm.openRuntimeModal.state)
	}
	if !strings.Contains(nm.openRuntimeModal.errMsg, "rpc unavailable") {
		t.Fatalf("errMsg = %q, want the fetch error surfaced", nm.openRuntimeModal.errMsg)
	}
}

func TestOpenRuntimeModal_ModelsLoadedIgnoredWhenModalClosed(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	m.openRuntimeModal = nil
	next, cmd := m.Update(modalModelsLoadedMsg{models: twoGGUFs()})
	nm := next.(Model)
	if nm.openRuntimeModal != nil || cmd != nil {
		t.Fatal("a late fetch reply must not reopen a closed modal")
	}
}

func TestOpenRuntimeModal_PickerCursorClampsAtEdges(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	m.openRuntimeModal.setPickModel(nil, "llama_server", twoGGUFs(), 64<<30)
	// Up at the top stays at 0.
	nm, _ := m.handleOpenRuntimeModalKey(keyPress("up"))
	if nm.openRuntimeModal.picker.Cursor() != 0 {
		t.Fatalf("cursor after up-at-top = %d, want 0", nm.openRuntimeModal.picker.Cursor())
	}
	// Down moves to 1; down again clamps at 1.
	nm, _ = nm.handleOpenRuntimeModalKey(keyPress("down"))
	nm, _ = nm.handleOpenRuntimeModalKey(keyPress("down"))
	if nm.openRuntimeModal.picker.Cursor() != 1 {
		t.Fatalf("cursor after down-at-bottom = %d, want 1", nm.openRuntimeModal.picker.Cursor())
	}
}

func TestOpenRuntimeModal_PickerEnterDispatchesAndCloses(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	m.openRuntimeModal.setPickModel(nil, "llama_server", twoGGUFs(), 64<<30)
	m.pendingRuntimeSwitch = "llama_server"
	nm, cmd := m.handleOpenRuntimeModalKey(keyPress("enter"))
	if nm.openRuntimeModal != nil {
		t.Fatal("enter should close the modal")
	}
	if nm.pendingRuntimeSwitch != "" {
		t.Fatal("enter should clear the pending switch")
	}
	if cmd == nil {
		t.Fatal("enter should dispatch the pick cmd")
	}
}

func TestOpenRuntimeModal_PickerEscClosesWithoutDispatch(t *testing.T) {
	m := modelWithModal(ambiguousStatus())
	m.openRuntimeModal.setPickModel(nil, "llama_server", twoGGUFs(), 64<<30)
	m.pendingRuntimeSwitch = "llama_server"
	nm, cmd := m.handleOpenRuntimeModalKey(keyPress("esc"))
	if nm.openRuntimeModal != nil || nm.pendingRuntimeSwitch != "" || cmd != nil {
		t.Fatal("esc should close, clear pending, and dispatch nothing")
	}
}

func TestOpenRuntimeModal_PickerViewListsModels(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newOpenRuntimeInstallModal(ambiguousStatus())
	mo.setPickModel(nil, "llama_server", twoGGUFs(), 64<<30)
	out := stripAnsiCSI(mo.View(styles, pal, 120, 40))
	for _, want := range []string{"pick a GGUF model", "qwen3-coder-30b-q4", "llama3.3-70b-q3", "Q4_K_M", "esc close"} {
		if !strings.Contains(out, want) {
			t.Fatalf("picker view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Install now") {
		t.Fatal("picker view must not offer Install now")
	}
}

func TestOpenRuntimeModal_ScanningViewShowsProgressNotInstall(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newOpenRuntimeInstallModal(ambiguousStatus())
	out := stripAnsiCSI(mo.View(styles, pal, 120, 40))
	if !strings.Contains(out, "Checking GGUF models") {
		t.Fatalf("scanning view missing title:\n%s", out)
	}
	if strings.Contains(out, "Install now") {
		t.Fatal("scanning view must not offer Install now")
	}
}

func TestOpenRuntimeModal_MistralMissingModelWithDefaultOffersSwitchDownload(t *testing.T) {
	p := theme.Cracker()
	m := Model{palette: p, styles: theme.NewStyles(p), currentOpenRuntime: "llama_server"}
	st := agentclient.OpenRuntimeStatus{
		Runtime:      "mistralrs",
		Missing:      "model",
		DefaultModel: "mistralrs:catalog:qwen3-14b",
		Message:      "mistralrs default model not downloaded",
	}
	next, cmd := m.Update(openOpenRuntimeInstallModalMsg{status: st, pending: "mistralrs"})
	if cmd != nil {
		t.Fatalf("mistralrs missing default should not start GGUF scan/install cmd")
	}
	nm := next.(Model)
	if nm.openRuntimeModal == nil {
		t.Fatalf("modal not opened")
	}
	if nm.openRuntimeModal.state != runtimeModalOfferSwitch {
		t.Fatalf("state = %v, want runtimeModalOfferSwitch", nm.openRuntimeModal.state)
	}
	if nm.openRuntimeModal.offerRuntime != "mistralrs" {
		t.Fatalf("offerRuntime = %q, want mistralrs", nm.openRuntimeModal.offerRuntime)
	}
	out := stripAnsiCSI(nm.openRuntimeModal.View(nm.styles, nm.palette, 100, 30))
	for _, want := range []string{
		"Switch to mistral.rs?",
		"mistral.rs needs its default model before it can run",
		"mistralrs:catalog:qwen3-14b",
		"Switch and download",
		"Stay on llama-server",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("modal should include %q, got:\n%s", want, out)
		}
	}
}

func TestOpenRuntimeModal_MistralMissingModelWithoutDefaultBrowsesModels(t *testing.T) {
	p := theme.Cracker()
	m := Model{palette: p, styles: theme.NewStyles(p), currentOpenRuntime: "llama_server"}
	st := agentclient.OpenRuntimeStatus{
		Runtime: "mistralrs",
		Missing: "model",
		Message: "mistralrs runtime: no default model configured",
	}
	next, cmd := m.Update(openOpenRuntimeInstallModalMsg{status: st, pending: "mistralrs"})
	if cmd != nil {
		t.Fatalf("mistralrs no-default should not start GGUF scan/install cmd")
	}
	nm := next.(Model)
	if nm.openRuntimeModal == nil {
		t.Fatalf("modal not opened")
	}
	if nm.openRuntimeModal.state != runtimeModalNeedsModel {
		t.Fatalf("state = %v, want runtimeModalNeedsModel", nm.openRuntimeModal.state)
	}
	out := stripAnsiCSI(nm.openRuntimeModal.View(nm.styles, nm.palette, 100, 30))
	if !strings.Contains(out, "mistral.rs needs a model") || !strings.Contains(out, "mistralrs.default_model") {
		t.Fatalf("modal should point mistral no-default users at model selection, got:\n%s", out)
	}
}
