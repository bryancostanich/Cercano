package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func TestLocalRuntimeModal_InitialStateIsIdle(t *testing.T) {
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{
		Missing:          "binary",
		Message:          "llama-server not on PATH",
		SuggestedCommand: "brew install llama.cpp",
	})
	if mo.state != runtimeModalIdle {
		t.Fatalf("initial state = %v, want runtimeModalIdle", mo.state)
	}
	if len(mo.logLines) != 0 {
		t.Fatalf("initial logLines = %v, want empty", mo.logLines)
	}
}

func TestLocalRuntimeModal_AppendLogTrimsCRLF(t *testing.T) {
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{})
	mo.appendLog("installing llama.cpp\r\n")
	mo.appendLog("done   ")
	if got := mo.logLines[0]; got != "installing llama.cpp" {
		t.Fatalf("logLines[0] = %q, want trimmed", got)
	}
	if got := mo.logLines[1]; got != "done" {
		t.Fatalf("logLines[1] = %q, want trimmed", got)
	}
}

func TestLocalRuntimeModal_SetFailedRecordsMessage(t *testing.T) {
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{})
	mo.state = runtimeModalRunning
	mo.setFailed("brew: command not found")
	if mo.state != runtimeModalFailed {
		t.Fatalf("state after setFailed = %v, want runtimeModalFailed", mo.state)
	}
	if mo.errMsg != "brew: command not found" {
		t.Fatalf("errMsg = %q, want the failure text", mo.errMsg)
	}
}

func TestLocalRuntimeModal_ViewIncludesTitleAndSuggestedCommand(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{
		Missing:          "binary",
		Message:          "llama-server not on PATH",
		SuggestedCommand: "brew install llama.cpp",
	})
	view := mo.View(styles, pal, 120, 40)
	if !strings.Contains(view, "Install llama-server?") {
		t.Fatalf("view missing title, got: %q", view)
	}
	if !strings.Contains(view, "brew install llama.cpp") {
		t.Fatalf("view missing suggested command, got: %q", view)
	}
	if !strings.Contains(view, "[Enter] Install now") {
		t.Fatalf("view missing action hint, got: %q", view)
	}
}

func TestLocalRuntimeModal_DimReflectsFrameSize(t *testing.T) {
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{})
	// Wide frame: box clamps to 80.
	w, h := mo.modalDim(200, 60)
	if w != 80 {
		t.Fatalf("wide frame width = %d, want clamp at 80", w)
	}
	if h > 24 {
		t.Fatalf("wide frame height = %d, want clamp at 24", h)
	}
	// Narrow frame: width shrinks but floors at 40.
	w, _ = mo.modalDim(50, 40)
	if w > 46 || w < 40 {
		t.Fatalf("narrow frame width = %d, want 40..46 range", w)
	}
	// Very short frame: height floors at 12.
	_, h = mo.modalDim(120, 10)
	if h != 12 {
		t.Fatalf("short frame height = %d, want floor at 12", h)
	}
}

func TestLocalRuntimeModal_ViewFailedShowsErrorAndRetry(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{
		Missing: "binary",
	})
	mo.setFailed("brew: command not found")
	view := mo.View(styles, pal, 120, 40)
	if !strings.Contains(view, "brew: command not found") {
		t.Fatalf("view missing error message, got: %q", view)
	}
	if !strings.Contains(view, "[Enter] Retry") {
		t.Fatalf("view missing retry action, got: %q", view)
	}
}

func TestLocalRuntimeModal_NeedsModelTitleAndActions(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newLocalRuntimeInstallModal(agentclient.LocalRuntimeStatus{Missing: "binary"})
	mo.setNeedsModel("install completed but detection still fails: llama-server detection: model: found 2 GGUF models; set llama_server.default_model to disambiguate")
	view := mo.View(styles, pal, 120, 40)

	// Title must NOT say "Install failed" — the install succeeded.
	if strings.Contains(view, "Install failed") {
		t.Fatalf("NeedsModel state must not render as Install failed:\n%s", view)
	}
	// Title must say llama-server is ready + point at the model step.
	if !strings.Contains(view, "llama-server ready") {
		t.Fatalf("NeedsModel title missing \"llama-server ready\" phrasing:\n%s", view)
	}
	// Actions must offer Close (Enter is not a Retry here — retrying can't
	// fix a missing model).
	if !strings.Contains(view, "[Esc] Close") {
		t.Fatalf("NeedsModel view must offer Close, got:\n%s", view)
	}
	if strings.Contains(view, "[Enter] Retry") {
		t.Fatalf("NeedsModel view must NOT offer Retry — install already succeeded:\n%s", view)
	}
	// Guidance for the two recovery paths must be present so the user
	// knows how to unblock themselves.
	if !strings.Contains(view, ".gguf") {
		t.Fatalf("NeedsModel view must mention the .gguf file convention:\n%s", view)
	}
	if !strings.Contains(view, "llama_server.default_model") {
		t.Fatalf("NeedsModel view must mention the config key:\n%s", view)
	}
}

func TestInstallErrorIsMissingModel_MatchesServerErrorShape(t *testing.T) {
	// The exact format comes from llamaserver.DetectError.Error() wrapped
	// by the server-side install handler. Coupling is intentional — the
	// server side is under our control, and this test is what catches a
	// server-side rename before it silently regresses the UX.
	cases := []struct {
		name string
		err  string
		want bool
	}{
		{
			name: "model missing after install",
			err:  "install completed but detection still fails: llama-server detection: model: found 2 GGUF models; set llama_server.default_model to disambiguate",
			want: true,
		},
		{
			name: "binary missing after install (should NOT be treated as needs-model)",
			err:  "install completed but detection still fails: llama-server detection: binary: exec: \"llama-server\" not found",
			want: false,
		},
		{
			name: "install itself failed (brew errored)",
			err:  "brew install llama.cpp: exit status 1",
			want: false,
		},
		{
			name: "empty",
			err:  "",
			want: false,
		},
	}
	for _, c := range cases {
		if got := installErrorIsMissingModel(c.err); got != c.want {
			t.Errorf("%s: installErrorIsMissingModel(%q) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}
