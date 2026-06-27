package agent

import (
	"os"
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

// A running agent holds the store in memory but the file on disk is the live
// source of truth: an external edit (a hand-edit, or a SetMode from another
// client sharing the singleton) must propagate without a restart.
func TestPermissionStore_LiveReloadOnExternalEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	s, err := LoadPermissionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode(ModeStrict); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModeStrict {
		t.Fatalf("expected strict after SetMode, got %s", s.Mode())
	}
	// Simulate an external edit flipping the file back to permissive.
	if err := os.WriteFile(path, []byte("mode: permissive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.Mode(); got != ModePermissive {
		t.Errorf("external edit not picked up: Mode()=%s want permissive", got)
	}
}

// A transiently missing or malformed file must never silently flip the gate
// open: the last-known mode is retained.
func TestPermissionStore_RetainsModeOnBadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	s, _ := LoadPermissionStore(path)
	if err := s.SetMode(ModeStrict); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := s.Mode(); got != ModeStrict {
		t.Errorf("missing file should retain strict, got %s", got)
	}
	if err := os.WriteFile(path, []byte("}{ not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.Mode(); got != ModeStrict {
		t.Errorf("malformed file should retain strict, got %s", got)
	}
}

func TestGateDecisionForMCP(t *testing.T) {
	cases := []struct {
		mode        PermissionMode
		isMCP       bool
		allowlisted bool
		want        bool // true == confirm required
	}{
		// Built-in W in permissive: silent (unchanged behavior).
		{ModePermissive, false, false, false},
		// MCP in permissive, not allowlisted: confirm.
		{ModePermissive, true, false, true},
		// MCP in permissive, allowlisted: silent.
		{ModePermissive, true, true, false},
		// MCP under bypass: silent.
		{ModeBypass, true, false, false},
		// MCP under strict, not allowlisted: confirm.
		{ModeStrict, true, false, true},
	}
	for _, c := range cases {
		got := GateDecisionForMCP(c.mode, llm.PermW, c.isMCP, c.allowlisted)
		if got != c.want {
			t.Errorf("mode=%s mcp=%v allow=%v: got %v want %v",
				c.mode, c.isMCP, c.allowlisted, got, c.want)
		}
	}
}

func TestMCPAllowlistGlob(t *testing.T) {
	p := filepath.Join(t.TempDir(), "permissions.yaml")
	s, _ := LoadPermissionStore(p)
	if err := s.AddMCPAllow("mcp__github__*"); err != nil {
		t.Fatal(err)
	}
	if !s.IsMCPAllowed("mcp__github__create_issue") {
		t.Fatal("glob should match")
	}
	if s.IsMCPAllowed("mcp__gitlab__create_issue") {
		t.Fatal("glob should not match other server")
	}
	// Persists across reload, preserving mode.
	if err := s.SetMode(ModeStrict); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadPermissionStore(p)
	if !s2.IsMCPAllowed("mcp__github__create_issue") {
		t.Fatal("allowlist not persisted")
	}
	if s2.Mode() != ModeStrict {
		t.Fatal("mode clobbered by allowlist write")
	}
}

func TestAddMCPAllow_Idempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "permissions.yaml")
	s, _ := LoadPermissionStore(p)

	// Call AddMCPAllow twice with the same pattern.
	if err := s.AddMCPAllow("mcp__github__*"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMCPAllow("mcp__github__*"); err != nil {
		t.Fatal(err)
	}

	// Reload from disk and count occurrences of the pattern.
	s2, err := LoadPermissionStore(p)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range s2.mcpAllow {
		if entry == "mcp__github__*" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 entry for pattern, got %d", count)
	}
}
