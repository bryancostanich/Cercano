package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/clients/cli/internal/wizard"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/config"
)

func newTestWizardPage(t *testing.T) *wizardPage {
	t.Helper()
	t.Setenv("CERCANO_WIZARD_STATE", filepath.Join(t.TempDir(), "wizard_state.yaml"))
	p := theme.BuiltinThemes()[0]
	wp := newWizardPage(nil, p.Palette, theme.NewStyles(p.Palette), 100, 40)
	// With a nil agent loadProviders() is a no-op, so seed the provider catalog
	// the cloud step renders. Mirrors the agent catalog's order/tiers (anthropic,
	// openai (responses), openai, …) so the step-navigation presses below land on
	// the providers the assertions expect.
	wp.providers = wizardTestProviders()
	return wp
}

// wizardTestProviders is the fixture catalog the wizard tests navigate. Only ID,
// Label, and Tier matter here: the eager commit path (commitKeyFn/commitMeridianFn)
// is stubbed in every test, so flavor/backend/base-URL are irrelevant to them.
func wizardTestProviders() []agentclient.CloudProvider {
	return []agentclient.CloudProvider{
		{ID: "anthropic", Label: "anthropic", Tier: "verified"},
		{ID: "openai-responses", Label: "openai (responses)", Tier: "untested"},
		{ID: "openai", Label: "openai", Tier: "untested"},
		{ID: "gemini", Label: "gemini", Tier: "verified"},
		{ID: "groq", Label: "groq", Tier: "untested"},
		{ID: "deepinfra", Label: "deepinfra", Tier: "untested"},
		{ID: "together", Label: "together", Tier: "untested"},
		{ID: "openrouter", Label: "openrouter", Tier: "untested"},
		{ID: "deepseek", Label: "deepseek", Tier: "untested"},
		{ID: "bedrock", Label: "bedrock", Tier: "coming_soon"},
	}
}

func press(t *testing.T, wp *wizardPage, code rune) bool {
	t.Helper()
	_, closed := wp.Update(tea.KeyPressMsg{Code: code})
	return closed
}

func TestWizardCloudPathEndToEnd(t *testing.T) {
	wp := newTestWizardPage(t)

	// Step 1 (locus, first): the cursor sits on the recommended cloud_primary.
	rows := wp.rows()
	if rows[wp.cursor].Key != "cloud_primary" {
		t.Fatalf("locus cursor: want cloud_primary, got %s", rows[wp.cursor].Key)
	}
	press(t, wp, tea.KeyEnter)
	if wp.state.Step != wizard.StepCloud {
		t.Fatalf("after locus: want %s, got %s", wizard.StepCloud, wp.state.Step)
	}

	// Step 2 phase 1: pick the first provider (anthropic).
	press(t, wp, tea.KeyEnter)
	if !wp.authPick {
		t.Fatal("provider selected: want auth-method phase")
	}
	if wp.state.CloudProvider != "anthropic" {
		t.Fatalf("provider: want anthropic, got %s", wp.state.CloudProvider)
	}
	// Phase 2: pick "sign in with Claude" (the first anthropic auth row).
	// It hands off to the loopback modal (owned by the root model) and
	// advances the wizard behind it — no eager profile commit here.
	press(t, wp, tea.KeyEnter)
	if wp.state.Step != wizard.StepOpen {
		t.Fatalf("after cloud: want %s, got %s (cloud_primary uses open)", wizard.StepOpen, wp.state.Step)
	}
	if wp.state.AuthMethod != "claude" {
		t.Fatalf("auth: want claude, got %s", wp.state.AuthMethod)
	}

	// Step 3 (open): autofill should have filled both sides for every tier.
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
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // open (second row)
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
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // open (second row)
	if wp.state.Step != wizard.StepOpen {
		t.Fatalf("open primary: want %s, got %s", wizard.StepOpen, wp.state.Step)
	}
	// Only open-side tier rows should appear later.
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

	press(t, wp, tea.KeyEnter) // pick cloud_primary (first row) → cloud step
	if closed := press(t, wp, tea.KeyEscape); closed {
		t.Fatal("esc on cloud step should go back, not close")
	}
	if wp.state.Step != wizard.StepLocus {
		t.Fatalf("esc: want %s, got %s", wizard.StepLocus, wp.state.Step)
	}
	if closed := press(t, wp, tea.KeyEscape); !closed {
		t.Fatal("esc on first step should close the page")
	}

	// A fresh page resumes from the persisted state.
	wp2 := newWizardPage(nil, p.Palette, theme.NewStyles(p.Palette), 100, 40)
	if wp2.state.Step != wizard.StepLocus {
		t.Fatalf("resume: want %s, got %s", wizard.StepLocus, wp2.state.Step)
	}
	if wp2.state.LocusMode != "cloud_primary" {
		t.Fatalf("resume: want preserved locus cloud_primary, got %q", wp2.state.LocusMode)
	}
}

func TestWizardTierPickerRecordsPick(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyDown)  // cloud_primary → open_only
	press(t, wp, tea.KeyEnter) // open_only → open step (skips cloud)

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
	if !rows[0].Selected {
		t.Errorf("first candidate: want Selected=true (autofilled pick), got false; hint=%q", rows[0].Hint)
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
	press(t, wp, tea.KeyDown)
	press(t, wp, tea.KeyEnter) // open (second row)
	before := wp.state.TierPicks["most_capable.open"]
	press(t, wp, tea.KeyEnter) // open picker
	press(t, wp, tea.KeyEscape)
	if wp.picker != nil {
		t.Fatal("esc: want picker closed")
	}
	if wp.state.TierPicks["most_capable.open"] != before {
		t.Error("esc: pick must be unchanged")
	}
	if wp.state.Step != wizard.StepOpen {
		t.Errorf("esc: page must stay on tiers, got %s", wp.state.Step)
	}
}

func TestWizardAPIKeyFlow(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyEnter) // cloud (first row)
	press(t, wp, tea.KeyDown)  // -> openai (responses)
	press(t, wp, tea.KeyDown)  // -> openai
	press(t, wp, tea.KeyEnter) // openai (third preset row)
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
	if wp.state.Step != wizard.StepOpen {
		t.Fatalf("after key: want %s, got %s", wizard.StepOpen, wp.state.Step)
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
	press(t, wp, tea.KeyEnter) // cloud (first row)
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

func TestWizardProfileModelFromRecs(t *testing.T) {
	recs := config.TierRecommendations{
		Version: 1,
		Cloud: map[string]config.TierCandidates{
			"anthropic": {
				config.TierEveryday: {"claude-opus-4-8", "claude-sonnet-4-6"},
			},
		},
	}
	// The profile model is the everyday-tier pick: "the default workhorse
	// for main chat" is exactly what profile.Model serves at request time.
	if got := wizardProfileModel(recs, "anthropic"); got != "claude-opus-4-8" {
		t.Fatalf("want everyday-tier first candidate, got %q", got)
	}
	// Unknown provider → no recommendation; caller leaves the model unset.
	if got := wizardProfileModel(recs, "nope"); got != "" {
		t.Fatalf("unknown provider: want empty, got %q", got)
	}
}

func TestWizardFinishUpdateCarriesCloudModel(t *testing.T) {
	// Cloud path: the everyday-cloud tier pick must land on the active
	// profile (via UpdateConfig's CloudModel → active-profile rebuild path),
	// or the profile serves requests with whatever model it was seeded with.
	u := wizardFinishUpdate(wizard.State{
		CloudProvider: "anthropic",
		LocusMode:     "cloud_primary",
		TierPicks:     map[string]string{"everyday.cloud": "claude-opus-4-8"},
	})
	if u.LocusMode != "cloud_primary" {
		t.Fatalf("locus patch wrong: %+v", u)
	}
	if u.CloudModel != "claude-opus-4-8" {
		t.Fatalf("want everyday.cloud pick as CloudModel, got %q", u.CloudModel)
	}
	// Open path: no cloud provider configured → no CloudModel patch (the
	// wantCloudRebuild branch errors without an active profile).
	u2 := wizardFinishUpdate(wizard.State{LocusMode: "open_primary"})
	if u2.CloudModel != "" {
		t.Fatalf("open path must not patch CloudModel, got %q", u2.CloudModel)
	}
}

func pressQ(t *testing.T, wp *wizardPage) bool {
	t.Helper()
	_, closed := wp.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	return closed
}

func TestWizardAbandonTrapdoor(t *testing.T) {
	wp := newTestWizardPage(t)
	press(t, wp, tea.KeyEnter) // cloud (first row) — state now persisted mid-run

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
	press(t, wp, tea.KeyEnter) // cloud (first row)

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
	press(t, wp, tea.KeyEnter) // cloud (first row)
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
	wp.state.LocusMode = "open_only"
	wp.state.Step = wizard.StepOpen
	wp.autofillTiers()
	v := wp.View()
	for _, want := range []string{"most-capable", "easy to change later", "step 2 of 2"} {
		if !strings.Contains(v, want) {
			t.Errorf("tiers view: missing %q", want)
		}
	}
}

func TestWizardOpenAutofillUsesCatalog(t *testing.T) {
	wp := newTestWizardPage(t)
	wp.catalog = agentclient.RuntimeModelCatalog{
		Models: []agentclient.RuntimeModel{
			{ID: "llama_server:catalog:qwen3-14b-q4_k_m", DisplayName: "Qwen3-14B Q4_K_M", Runtime: "llama_server", Source: "catalog"},
			{ID: "llama_server:catalog:phi-4-mini-instruct-q4_k_m", DisplayName: "Phi-4-mini Instruct Q4_K_M", Runtime: "llama_server", Source: "catalog"},
			{ID: "llama_server:catalog:nomic-embed-text-v1.5-f16", DisplayName: "Nomic Embed Text v1.5 f16", Runtime: "llama_server", Source: "catalog"},
		},
		RecommendedOpenModels: map[string]string{
			"most_capable":    "llama_server:catalog:qwen3-14b-q4_k_m",
			"everyday":        "llama_server:catalog:qwen3-14b-q4_k_m",
			"fast_light":      "llama_server:catalog:phi-4-mini-instruct-q4_k_m",
			"fast_light_text": "llama_server:catalog:phi-4-mini-instruct-q4_k_m",
			"embedding":       "llama_server:catalog:nomic-embed-text-v1.5-f16",
		},
	}
	wp.catalogOK = true
	wp.state.TierPicks = nil

	wp.autofillTiers()

	// Open slots must hold the RAM-tiered curated display names, not the shipped
	// open recs (which recommend the gate-incompatible qwen3-coder-next).
	if got := wp.state.TierPicks["most_capable.open"]; got != "Qwen3-14B Q4_K_M" {
		t.Fatalf("most_capable.open: want curated display name, got %q", got)
	}
	if got := wp.state.TierPicks["fast_light.open"]; got != "Phi-4-mini Instruct Q4_K_M" {
		t.Fatalf("fast_light.open: want curated display name, got %q", got)
	}
	// The embedding slot isn't in wizardTierOrder; autofillTiers must still fill
	// it from the catalog recommendation, else the embedding row shows "—".
	if got := wp.state.TierPicks["embedding.open"]; got != "Nomic Embed Text v1.5 f16" {
		t.Fatalf("embedding.open: want the curated embedding display name, got %q", got)
	}
	if strings.Contains(fmt.Sprintf("%v", wp.state.TierPicks), "qwen3-coder-next") {
		t.Errorf("open picks must not carry the incompatible recs model: %v", wp.state.TierPicks)
	}
}

func TestWizardEnrollOpenDownloads(t *testing.T) {
	wp := newTestWizardPage(t)
	wp.catalog = agentclient.RuntimeModelCatalog{
		Models: []agentclient.RuntimeModel{
			{ID: "llama_server:catalog:qwen3-14b-q4_k_m", DisplayName: "Qwen3-14B Q4_K_M", Runtime: "llama_server", Source: "catalog", DownloadState: "not_downloaded"},
			{ID: "llama_server:catalog:phi-4-mini-instruct-q4_k_m", DisplayName: "Phi-4-mini Instruct Q4_K_M", Runtime: "llama_server", Source: "catalog", DownloadState: "downloaded"},
		},
	}
	wp.catalogOK = true
	wp.state.TierPicks = map[string]string{
		"most_capable.open":  "Qwen3-14B Q4_K_M",
		"everyday.open":      "Qwen3-14B Q4_K_M",           // duplicate of most_capable -> one download
		"fast_light.open":    "Phi-4-mini Instruct Q4_K_M", // already downloaded -> skipped
		"most_capable.cloud": "claude-opus-4-8",            // cloud slot -> ignored
	}
	var got []string
	wp.downloadFn = func(_ context.Context, runtime, modelID string) error {
		if runtime != "llama_server" {
			t.Errorf("runtime: want llama_server, got %q", runtime)
		}
		got = append(got, modelID)
		return nil
	}

	wp.enrollOpenDownloads(context.Background())

	// Exactly the distinct, not-yet-downloaded curated id, resolved from its
	// display name: duplicates deduped, downloaded skipped, cloud ignored.
	if len(got) != 1 || got[0] != "llama_server:catalog:qwen3-14b-q4_k_m" {
		t.Fatalf("want the single distinct not-downloaded curated id, got %v", got)
	}
}

func TestWrapWords(t *testing.T) {
	// The bug: a long annotation ran off the right edge instead of wrapping.
	// wrapWords must break on spaces, never exceed width, and preserve every
	// word in order.
	long := "highest-quality frontier model in the cloud; open co-processor for background work (recommended)"
	for _, width := range []int{10, 20, 40, 89} {
		lines := wrapWords(long, width)
		if len(lines) < 2 {
			t.Errorf("width %d: expected the long annotation to wrap, got %d line(s)", width, len(lines))
		}
		for _, ln := range lines {
			// A single word longer than width is allowed to overflow on its
			// own line; only reject a line that packed more than it should.
			if n := len([]rune(ln)); n > width && strings.Contains(strings.TrimSpace(ln), " ") {
				if firstWordFits(ln, width) {
					t.Errorf("width %d: line %q exceeds width %d", width, ln, width)
				}
			}
		}
		if got := strings.Join(lines, " "); got != long {
			t.Errorf("width %d: words not preserved:\n got %q\nwant %q", width, got, long)
		}
	}

	// width < 1 yields the whole string on one line (fallback, no panic).
	if got := wrapWords(long, 0); len(got) != 1 || got[0] != long {
		t.Errorf("width 0: want single unbroken line, got %v", got)
	}
	// A word longer than width stands alone rather than being split.
	if got := wrapWords("supercalifragilistic ok", 5); len(got) != 2 || got[0] != "supercalifragilistic" {
		t.Errorf("oversized word: want it alone on its line, got %v", got)
	}
	if got := wrapWords("", 10); got != nil {
		t.Errorf("empty: want nil, got %v", got)
	}
}

// firstWordFits reports whether line's first word fits within width, i.e.
// the line could legitimately have been packed tighter.
func firstWordFits(line string, width int) bool {
	fields := strings.Fields(line)
	return len(fields) > 0 && len([]rune(fields[0])) <= width
}
