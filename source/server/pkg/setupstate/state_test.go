package setupstate

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFreshHasNoPreviousAnswers(t *testing.T) {
	t.Setenv("CERCANO_WIZARD_STATE", filepath.Join(t.TempDir(), "wizard.yaml"))
	old := State{Step: StepDone, LocusMode: "cloud_only", CloudProvider: "old", AuthMethod: "api_key", TierPicks: map[string]string{"everyday.open": "old-model"}, CloudPicksEdited: map[string]bool{"premium": true}, Baseline: &Baseline{ActiveProfile: "old"}}
	if err := Save(old); err != nil {
		t.Fatal(err)
	}
	fresh := Fresh()
	if !reflect.DeepEqual(fresh, State{Step: StepLocus}) {
		t.Fatalf("fresh=%+v", fresh)
	}
	if err := Save(fresh); err != nil {
		t.Fatal(err)
	}
	got, ok := Load()
	if !ok || !reflect.DeepEqual(got, fresh) {
		t.Fatalf("loaded=%+v ok=%v", got, ok)
	}
	data, err := os.ReadFile(StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("old")) {
		t.Fatal("old baseline persisted")
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok = Load(); ok {
		t.Fatal("completed run still resumes")
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
}
func TestPathAndExistingSchemaCompatibility(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("CERCANO_WIZARD_STATE", "")
	if StatePath() != filepath.Join(root, "cercano", "wizard_state.yaml") {
		t.Fatal(StatePath())
	}
	p := filepath.Join(root, "override.yaml")
	t.Setenv("CERCANO_WIZARD_STATE", p)
	data := []byte("step: done\nlocus_mode: cloud_primary\ncloud_provider: fixture\nauth_method: api_key\ntier_picks: {everyday.open: local-model}\ncloud_picks_edited: {premium: true}\nbaseline:\n  active_profile: old\n  profiles:\n  - name: old\n    flavor: messages\n    details_captured: true\n    image_model: vision\n    tier_overrides: {premium: model}\n")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, ok := Load()
	if !ok || s.Step != StepDone || s.Baseline.Profiles[0].ImageModel != "vision" {
		t.Fatal("existing persisted state lost")
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	var before, after any
	yaml.Unmarshal(data, &before)
	yaml.Unmarshal(got, &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("wire schema changed")
	}
}
