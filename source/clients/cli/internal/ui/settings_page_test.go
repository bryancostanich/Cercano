package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func TestSettingsColorCommitEmitsMsg(t *testing.T) {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 40}
	// Build a form whose commit hook is the page's real router.
	sp.form = form.New([]form.Section{{Title: "UI / Theme", Fields: []form.Field{
		form.NewSelect("accent-color", "accent-color", accentColorOptions(), "palette:accent"),
	}}})
	sp.form.OnCommit = sp.onCommit

	status, cmd, err := sp.onCommit("accent-color", "palette:info")
	if err != nil {
		t.Fatalf("onCommit err: %v", err)
	}
	if cmd == nil {
		t.Fatal("color commit should return a cmd")
	}
	msg := cmd()
	cm, ok := msg.(settingsColorMsg)
	if !ok || cm.token != "palette:info" {
		t.Fatalf("expected settingsColorMsg{palette:info}, got %#v", msg)
	}
	_ = status
}

func TestSettingsPageImplementsContentPage(t *testing.T) {
	var _ contentPage = (*settingsPage)(nil)
	var _ contentPageScroller = (*settingsPage)(nil)
}

func TestSettingsPageViewRenders(t *testing.T) {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 40}
	sp.form = form.New(buildSettingsSections(&agentclient.Config{OpenModel: "qwen", Port: "50052"}, "permissive", "palette:accent"))
	out := sp.View()
	if !strings.Contains(out, "Open Model") || !strings.Contains(out, "permission-mode") {
		t.Fatalf("settings View missing content:\n%s", out)
	}
}

func TestSettingsPageViewFitsViewport(t *testing.T) {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 30}
	sp.form = form.New(buildSettingsSections(&agentclient.Config{OpenModel: "x", Port: "50052"}, "permissive", "palette:accent"))
	out := sp.View()
	gotRows := strings.Count(out, "\n") + 1
	if gotRows != sp.viewportHeight() {
		t.Fatalf("rendered rows = %d, want viewportHeight %d (must reserve root chrome, not full height)", gotRows, sp.viewportHeight())
	}
}

func TestSettingsPageKeyboardScrollFollowsCursor(t *testing.T) {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	// Small height so the form overflows the viewport.
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 14}
	sp.form = form.New(buildSettingsSections(&agentclient.Config{OpenModel: "x", Port: "50052"}, "permissive", "palette:accent"))
	for i := 0; i < 30; i++ { // navigate to the bottom
		sp.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if sp.offset == 0 {
		t.Fatal("scroll offset should advance as keyboard cursor descends past the fold")
	}
}
