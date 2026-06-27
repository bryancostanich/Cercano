package ui

import (
	"strings"
	"testing"

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
	sp.form = form.New(buildSettingsSections(&agentclient.Config{LocalModel: "qwen", Port: "50052"}, "permissive", "palette:accent"))
	out := sp.View()
	if !strings.Contains(out, "Local Model") || !strings.Contains(out, "permission-mode") {
		t.Fatalf("settings View missing content:\n%s", out)
	}
}
