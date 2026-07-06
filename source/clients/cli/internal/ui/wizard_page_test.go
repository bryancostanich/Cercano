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
	return newWizardPage(nil, p.Palette, theme.NewStyles(p.Palette), 100, 40)
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
	// Phase 2: pick meridian (commits the profile eagerly; stub it).
	wp.commitMeridianFn = func() error { return nil }
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
	wp := newWizardPage(nil, p.Palette, theme.NewStyles(p.Palette), 100, 40)

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
	wp2 := newWizardPage(nil, p.Palette, theme.NewStyles(p.Palette), 100, 40)
	if wp2.state.Step != wizard.StepPrimary {
		t.Fatalf("resume: want %s, got %s", wizard.StepPrimary, wp2.state.Step)
	}
	if wp2.state.PrimarySide != wizard.SideCloud {
		t.Fatalf("resume: want preserved side %s, got %q", wizard.SideCloud, wp2.state.PrimarySide)
	}
}

func TestWizardTierPickerRecordsPick(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyEnter) // open primary
	press(t, wp, tea.KeyEnter) // recommended locus → tiers

	// Enter on the first tier row opens the picker.
	press(t, wp, tea.KeyEnter)
	if wp.picker == nil {
		t.Fatal("enter on tier row: want picker open")
	}
	// First candidate is the recommendation, already the autofilled pick.
	rows := wp.tierPickerCandidates("most_capable.open")
	if len(rows) < 2 {
		t.Fatalf("picker: want recommendation + clear rows, got %d", len(rows))
	}
	if rows[0].Hint != "current" {
		t.Errorf("first candidate: want hint current (autofilled), got %q", rows[0].Hint)
	}
	if rows[len(rows)-1].Key != "-" {
		t.Errorf("last row: want clear row, got %q", rows[len(rows)-1].Key)
	}

	// Down to the clear row, select: pick removed, picker closed, persisted.
	for range rows {
		press(t, wp, tea.KeyDown)
	}
	press(t, wp, tea.KeyEnter)
	if wp.picker != nil {
		t.Fatal("select: want picker closed")
	}
	if _, ok := wp.state.TierPicks["most_capable.open"]; ok {
		t.Error("clear: pick should be removed")
	}
	st, ok := wizard.Load()
	if !ok {
		t.Fatal("want persisted state")
	}
	if _, exists := st.TierPicks["most_capable.open"]; exists {
		t.Error("clear: persisted state should not carry the removed pick")
	}

	// Reopen and pick the recommendation again.
	press(t, wp, tea.KeyEnter)
	press(t, wp, tea.KeyEnter)
	if wp.state.TierPicks["most_capable.open"] == "" {
		t.Error("pick: want slot filled from picker selection")
	}
}

func TestWizardPickerEscClosesWithoutChange(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyEnter) // open primary
	press(t, wp, tea.KeyEnter) // locus → tiers
	before := wp.state.TierPicks["most_capable.open"]
	press(t, wp, tea.KeyEnter) // open picker
	press(t, wp, tea.KeyEscape)
	if wp.picker != nil {
		t.Fatal("esc: want picker closed")
	}
	if wp.state.TierPicks["most_capable.open"] != before {
		t.Error("esc: pick must be unchanged")
	}
	if wp.state.Step != wizard.StepTiers {
		t.Errorf("esc: page must stay on tiers, got %s", wp.state.Step)
	}
}

func TestWizardAPIKeyFlow(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // openai (second preset row)
	if wp.state.CloudProvider != "openai" {
		t.Fatalf("provider: want openai, got %s", wp.state.CloudProvider)
	}
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // api_key (second auth row)
	if !wp.keyEntry {
		t.Fatal("api_key: want key prompt open")
	}

	// Empty key: prompt, no commit.
	press(t, wp, tea.KeyEnter)
	if !strings.Contains(wp.status, "enter a key") {
		t.Errorf("empty key: status %q", wp.status)
	}

	// Failing commit keeps the prompt.
	wp.commitKeyFn = func(string) error { return fmt.Errorf("boom") }
	wp.keyInput.SetValue("sk-test-123")
	press(t, wp, tea.KeyEnter)
	if !wp.keyEntry || !strings.Contains(wp.status, "key setup failed") {
		t.Fatalf("failed commit: want prompt kept + error status, got keyEntry=%v status=%q", wp.keyEntry, wp.status)
	}

	// Successful commit records the key agent-side and advances.
	var got string
	wp.commitKeyFn = func(k string) error { got = k; return nil }
	press(t, wp, tea.KeyEnter)
	if got != "sk-test-123" {
		t.Errorf("commit: want key passed through, got %q", got)
	}
	if wp.state.Step != wizard.StepLocus {
		t.Fatalf("after key: want %s, got %s", wizard.StepLocus, wp.state.Step)
	}

	// The key must never be persisted in wizard state.
	st, ok := wizard.Load()
	if !ok {
		t.Fatal("want persisted state")
	}
	if strings.Contains(fmt.Sprintf("%+v", st), "sk-test-123") {
		t.Error("resume state must not contain the API key")
	}
}

func TestWizardKeyEntryEscReturnsToAuthPick(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud
	press(t, wp, tea.KeyEnter) // anthropic
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // api_key
	if !wp.keyEntry {
		t.Fatal("want key prompt")
	}
	press(t, wp, tea.KeyEscape)
	if wp.keyEntry {
		t.Fatal("esc: want prompt closed")
	}
	if !wp.authPick {
		t.Fatal("esc: want auth-method screen back")
	}
}

func TestWizardMeridianCommit(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud
	press(t, wp, tea.KeyEnter) // anthropic

	wp.commitMeridianFn = func() error { return fmt.Errorf("proxy down") }
	press(t, wp, tea.KeyEnter) // meridian (first auth row)
	if wp.state.Step != wizard.StepCloud || !strings.Contains(wp.status, "meridian setup failed") {
		t.Fatalf("failed meridian: want stay on cloud + error, got step=%s status=%q", wp.state.Step, wp.status)
	}

	called := false
	wp.commitMeridianFn = func() error { called = true; return nil }
	press(t, wp, tea.KeyEnter)
	if !called {
		t.Error("meridian: commit not called")
	}
	if wp.state.Step != wizard.StepLocus {
		t.Fatalf("after meridian: want %s, got %s", wizard.StepLocus, wp.state.Step)
	}
}

func pressQ(t *testing.T, wp *wizardPage) bool {
	t.Helper()
	_, closed := wp.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	return closed
}

func TestWizardAbandonTrapdoor(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud — state now persisted mid-run

	rolledBack := false
	wp.rollbackFn = func() error { rolledBack = true; return nil }

	// First q only arms and asks.
	if closed := pressQ(t, wp); closed {
		t.Fatal("first q: page must stay open")
	}
	if !wp.abandonArmed || !strings.Contains(wp.status, "abandon setup?") {
		t.Fatalf("first q: want armed + confirm status, got armed=%v status=%q", wp.abandonArmed, wp.status)
	}
	if rolledBack {
		t.Fatal("first q: must not roll back yet")
	}

	// Second q rolls back, clears the resume file, closes.
	if closed := pressQ(t, wp); !closed {
		t.Fatal("second q: want page closed")
	}
	if !rolledBack {
		t.Error("second q: rollbackFn not called")
	}
	if _, ok := wizard.Load(); ok {
		t.Error("abandon: resume state should be cleared")
	}
}

func TestWizardAbandonDisarmsOnOtherKey(t *testing.T) {
	wp := newTestWizardPage(t)
	wp.rollbackFn = func() error { t.Fatal("disarmed abandon must not roll back"); return nil }

	pressQ(t, wp)
	press(t, wp, tea.KeyDown) // any other key disarms
	if wp.abandonArmed {
		t.Fatal("other key: want disarmed")
	}
	if wp.status != "" {
		t.Errorf("other key: confirm status should clear, got %q", wp.status)
	}
	// The next q starts the confirm over instead of closing.
	if closed := pressQ(t, wp); closed {
		t.Fatal("q after disarm: must arm again, not close")
	}
}

func TestWizardAbandonRollbackFailureStaysOpen(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud

	wp.rollbackFn = func() error { return fmt.Errorf("agent unreachable") }
	pressQ(t, wp)
	if closed := pressQ(t, wp); closed {
		t.Fatal("rollback failure: page must stay open")
	}
	if !strings.Contains(wp.status, "could not undo") {
		t.Errorf("rollback failure: status %q should surface the error", wp.status)
	}
	if _, ok := wizard.Load(); !ok {
		t.Error("rollback failure: resume state must be preserved for retry")
	}

	// Retry after the failure succeeds and closes.
	wp.rollbackFn = func() error { return nil }
	pressQ(t, wp)
	if closed := pressQ(t, wp); !closed {
		t.Fatal("retry: want page closed")
	}
}

func TestWizardKeyEntryQTypesIntoPrompt(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // cloud
	press(t, wp, tea.KeyEnter) // anthropic
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // api_key → masked prompt
	if !wp.keyEntry {
		t.Fatal("want key prompt open")
	}
	if closed := pressQ(t, wp); closed {
		t.Fatal("q in key prompt: must not close the page")
	}
	if wp.abandonArmed {
		t.Fatal("q in key prompt: must not arm abandon")
	}
	if got := wp.keyInput.Value(); got != "q" {
		t.Errorf("q in key prompt: want it typed into the input, got %q", got)
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
