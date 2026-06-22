package agent

import (
	"path/filepath"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestGateDecision_Strict_AllConfirm(t *testing.T) {
	cases := []struct {
		tier llm.Permission
		want bool
	}{
		{llm.PermR, false},
		{llm.PermW, true},
		{llm.PermX, true},
	}
	for _, c := range cases {
		got := GateDecision(ModeStrict, c.tier)
		if got != c.want {
			t.Errorf("Strict %s: got %v want %v", c.tier, got, c.want)
		}
	}
}

func TestGateDecision_Permissive(t *testing.T) {
	cases := []struct {
		tier llm.Permission
		want bool
	}{
		{llm.PermR, false},
		{llm.PermW, false},
		{llm.PermX, true},
	}
	for _, c := range cases {
		got := GateDecision(ModePermissive, c.tier)
		if got != c.want {
			t.Errorf("Permissive %s: got %v want %v", c.tier, got, c.want)
		}
	}
}

func TestGateDecision_Bypass_NoConfirm(t *testing.T) {
	for _, tier := range []llm.Permission{llm.PermR, llm.PermW, llm.PermX} {
		if GateDecision(ModeBypass, tier) {
			t.Errorf("Bypass %s: should not require confirm", tier)
		}
	}
}

func TestParseMode(t *testing.T) {
	cases := map[string]PermissionMode{
		"strict":     ModeStrict,
		"permissive": ModePermissive,
		"bypass":     ModeBypass,
	}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q): %v %v", in, got, err)
		}
	}
	if _, err := ParseMode("garbage"); err == nil {
		t.Errorf("expected error for garbage mode")
	}
}

func TestPermissionStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	s, err := LoadPermissionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModePermissive {
		t.Errorf("default mode should be permissive, got %s", s.Mode())
	}
	if err := s.SetMode(ModeStrict); err != nil {
		t.Fatal(err)
	}
	// reload
	s2, _ := LoadPermissionStore(path)
	if s2.Mode() != ModeStrict {
		t.Errorf("mode did not persist: %s", s2.Mode())
	}
}
