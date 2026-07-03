package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

var errTestBoom = errors.New("boom")

// newAsyncTestPage builds a settingsPage with a real form wired to the page's
// commit router, mirroring TestSettingsColorCommitEmitsMsg. agent stays nil —
// the async paths under test never reach an RPC (pending-guard and
// completion handling are all client-side).
func newAsyncTestPage() *settingsPage {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 40}
	sp.form = form.New(buildSettingsSections(&agentclient.Config{OpenModel: "qwen", Port: "50052"}, "permissive", "palette:accent"))
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = func() []form.Section {
		return buildSettingsSections(&agentclient.Config{OpenModel: "qwen", Port: "50052"}, sp.mode, "palette:accent")
	}
	return sp
}

func TestSettingsCommitConfig_ReturnsPendingStatusAndCmd(t *testing.T) {
	sp := newAsyncTestPage()
	status, cmd, err := sp.onCommit("local-model", "llama3")
	if err != nil {
		t.Fatalf("onCommit err: %v", err)
	}
	if !sp.pendingCommit {
		t.Fatal("commitConfig should set pendingCommit")
	}
	if cmd == nil {
		t.Fatal("commitConfig should return a background cmd")
	}
	if !strings.Contains(status, "applying") {
		t.Fatalf("expected an in-progress status, got %q", status)
	}
}

func TestSettingsCommitConfig_ReentryIsNoopWhilePending(t *testing.T) {
	sp := newAsyncTestPage()
	if _, _, err := sp.onCommit("local-model", "llama3"); err != nil {
		t.Fatalf("first commit err: %v", err)
	}
	status, cmd, err := sp.onCommit("local-model", "qwen3")
	if err != nil {
		t.Fatalf("re-entry err: %v", err)
	}
	if cmd != nil {
		t.Fatal("re-entry while pending must not start a second background commit")
	}
	if !strings.Contains(status, "still applying") {
		t.Fatalf("expected still-applying status, got %q", status)
	}
}

func TestSettingsApplyCommitDone_InstallsFreshConfigAndClearsPending(t *testing.T) {
	sp := newAsyncTestPage()
	sp.pendingCommit = true
	fresh := &agentclient.Config{OpenModel: "llama3", Port: "50052"}
	sp.applyCommitDone(settingsCommitDoneMsg{status: "updated", cfg: fresh, mode: "strict"})
	if sp.pendingCommit {
		t.Fatal("applyCommitDone must clear pendingCommit")
	}
	if sp.cfg != fresh || sp.mode != "strict" {
		t.Fatal("applyCommitDone must install the freshly-fetched config and mode")
	}
}

func TestSettingsApplyCommitDone_ErrorKeepsCachedConfig(t *testing.T) {
	sp := newAsyncTestPage()
	sp.pendingCommit = true
	cached := &agentclient.Config{OpenModel: "qwen", Port: "50052"}
	sp.cfg = cached
	sp.applyCommitDone(settingsCommitDoneMsg{err: errTestBoom})
	if sp.pendingCommit {
		t.Fatal("applyCommitDone must clear pendingCommit on error too")
	}
	if sp.cfg != cached {
		t.Fatal("a failed commit must not clobber the cached config")
	}
}

func TestSettingsApplyCommitDone_RunsFollowup(t *testing.T) {
	sp := newAsyncTestPage()
	sp.pendingCommit = true
	ran := false
	cmd := sp.applyCommitDone(settingsCommitDoneMsg{
		status:   "permission mode: strict",
		mode:     "strict",
		followup: func() tea.Msg { ran = true; return nil },
	})
	if cmd == nil {
		t.Fatal("applyCommitDone must forward the followup cmd")
	}
	cmd()
	if !ran {
		t.Fatal("followup cmd did not run")
	}
}

func TestSettingsSpinnerTick_ReArmsOnlyWhilePending(t *testing.T) {
	sp := newAsyncTestPage()
	sp.pendingCommit = true
	if sp.applySpinnerTick() == nil {
		t.Fatal("spinner tick must re-arm while pending")
	}
	sp.pendingCommit = false
	if sp.applySpinnerTick() != nil {
		t.Fatal("spinner tick must stop once the commit completed")
	}
}
