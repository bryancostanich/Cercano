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
