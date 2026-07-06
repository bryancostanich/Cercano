package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/clients/cli/internal/wizard"
)

func newTestWizardPage(t *testing.T) *wizardPage {
	t.Helper()
	t.Setenv("CERCANO_WIZARD_STATE", filepath.Join(t.TempDir(), "wizard_state.yaml"))
	p := theme.BuiltinThemes()[0]
	return newWizardPage(nil, theme.NewStyles(p.Palette), 100, 40)
}

func press(t *testing.T, wp *wizardPage, code rune) bool {
	t.Helper()
	_, closed := wp.Update(tea.KeyPressMsg{Code: code})
	return closed
}

func TestWizardCloudPathEndToEnd(t *testing.T) {
	wp := newTestWizardPage(t)

	// Step 1: pick cloud (second row).
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter)
	if wp.state.Step != wizard.StepCloud {
		t.Fatalf("after primary: want %s, got %s", wizard.StepCloud, wp.state.Step)
	}

	// Step 2 phase 1: pick the first provider (anthropic).
	press(t, wp, tea.KeyEnter)
	if !wp.authPick {
		t.Fatal("provider selected: want auth-method phase")
	}
	if wp.state.CloudProvider != "anthropic" {
		t.Fatalf("provider: want anthropic, got %s", wp.state.CloudProvider)
	}
	// Phase 2: pick meridian.
	press(t, wp, tea.KeyEnter)
	if wp.state.Step != wizard.StepLocus {
		t.Fatalf("after cloud: want %s, got %s", wizard.StepLocus, wp.state.Step)
	}
	if wp.state.AuthMethod != "meridian" {
		t.Fatalf("auth: want meridian, got %s", wp.state.AuthMethod)
	}

	// Step 3: cursor should sit on the recommended cloud_primary row.
	rows := wp.rows()
	if rows[wp.cursor].Key != "cloud_primary" {
		t.Fatalf("locus cursor: want cloud_primary, got %s", rows[wp.cursor].Key)
	}
	press(t, wp, tea.KeyEnter)
	if wp.state.Step != wizard.StepTiers {
		t.Fatalf("after locus: want %s, got %s", wizard.StepTiers, wp.state.Step)
	}

	// Step 4: autofill should have filled both sides for every tier.
	if wp.state.TierPicks["most_capable.cloud"] == "" {
		t.Error("autofill: most_capable.cloud empty")
	}
	if wp.state.TierPicks["fast_light_text.open"] == "" {
		t.Error("autofill: fast_light_text.open empty")
	}
	// Continue is the last row.
	for range wp.rows() {
		press(t, wp, tea.KeyDown)
	}
	press(t, wp, tea.KeyEnter)
	if wp.state.Step != wizard.StepDone {
		t.Fatalf("after tiers: want %s, got %s", wizard.StepDone, wp.state.Step)
	}

	// Finish applies the config, clears the resume file, closes the page.
	applied := false
	wp.applyFn = func() error { applied = true; return nil }
	if closed := press(t, wp, tea.KeyEnter); !closed {
		t.Fatal("finish: want page closed")
	}
	if !applied {
		t.Error("finish: applyFn not called")
	}
	if _, ok := wizard.Load(); ok {
		t.Error("finish: resume state should be cleared")
	}
}

func TestWizardApplyFailureKeepsState(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyEnter) // open primary
	press(t, wp, tea.KeyEnter) // recommended locus
	for range wp.rows() {
		press(t, wp, tea.KeyDown)
	}
	press(t, wp, tea.KeyEnter) // continue → done

	wp.applyFn = func() error { return fmt.Errorf("agent unreachable") }
	if closed := press(t, wp, tea.KeyEnter); closed {
		t.Fatal("apply failure: page must stay open")
	}
	if !strings.Contains(wp.status, "apply failed") {
		t.Errorf("apply failure: status %q should surface the error", wp.status)
	}
	if _, ok := wizard.Load(); !ok {
		t.Error("apply failure: resume state must be preserved for retry")
	}

	// Retry after the failure succeeds and closes.
	wp.applyFn = func() error { return nil }
	if closed := press(t, wp, tea.KeyEnter); !closed {
		t.Fatal("retry: want page closed")
	}
}

func TestWizardOpenPathSkipsCloud(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyEnter) // open is the first row
	if wp.state.Step != wizard.StepLocus {
		t.Fatalf("open primary: want %s, got %s", wizard.StepLocus, wp.state.Step)
	}
	// Only open-side tier rows should appear later.
	press(t, wp, tea.KeyEnter) // accept recommended open_primary
	for _, r := range wp.rows() {
		if strings.HasSuffix(r.Key, "."+wizard.SideCloud) {
			t.Errorf("open path: unexpected cloud tier row %s", r.Key)
		}
	}
}

func TestWizardEscResumesMidRun(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "wizard_state.yaml")
	t.Setenv("CERCANO_WIZARD_STATE", statePath)
	p := theme.BuiltinThemes()[0]
	wp := newWizardPage(nil, theme.NewStyles(p.Palette), 100, 40)

	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud
	if closed := press(t, wp, tea.KeyEscape); closed {
		t.Fatal("esc on cloud step should go back, not close")
	}
	if wp.state.Step != wizard.StepPrimary {
		t.Fatalf("esc: want %s, got %s", wizard.StepPrimary, wp.state.Step)
	}
	if closed := press(t, wp, tea.KeyEscape); !closed {
		t.Fatal("esc on first step should close the page")
	}

	// A fresh page resumes from the persisted state.
	wp2 := newWizardPage(nil, theme.NewStyles(p.Palette), 100, 40)
	if wp2.state.Step != wizard.StepPrimary {
		t.Fatalf("resume: want %s, got %s", wizard.StepPrimary, wp2.state.Step)
	}
	if wp2.state.PrimarySide != wizard.SideCloud {
		t.Fatalf("resume: want preserved side %s, got %q", wizard.SideCloud, wp2.state.PrimarySide)
	}
}

func TestWizardViewRendersTierPurposes(t *testing.T) {
	wp := newTestWizardPage(t)
	wp.state.PrimarySide = wizard.SideOpen
	wp.state.Step = wizard.StepTiers
	wp.autofillTiers()
	v := wp.View()
	for _, want := range []string{"most-capable", "easy to change later", "step 3 of 3"} {
		if !strings.Contains(v, want) {
			t.Errorf("tiers view: missing %q", want)
		}
	}
}
