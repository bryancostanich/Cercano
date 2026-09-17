package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// A file-less store must honor the allowlist it was handed. Regression: the
// worker built NewStaticPermissionStore (no path), and IsMCPAllowed read the
// allowlist off disk on every call — os.ReadFile("") always fails, so mcpAllow
// stayed nil and EVERY allowlisted MCP tool re-prompted in a worker turn.
func TestStaticStoreHonorsSuppliedMCPAllowlist(t *testing.T) {
	s := NewStaticPermissionStoreWithMCPAllow(ModePermissive, []string{"github__*", "exact__tool"})

	for _, name := range []string{"github__create_issue", "exact__tool"} {
		if !s.IsMCPAllowed(name) {
			t.Errorf("IsMCPAllowed(%q) = false, want true — supplied allowlist ignored", name)
		}
	}
	if s.IsMCPAllowed("other__tool") {
		t.Error("IsMCPAllowed(other__tool) = true, want false — non-matching tool must not be allowlisted")
	}
}

// The no-allowlist constructor must deny everything rather than read stray disk
// state. "Nothing allowlisted" is the safe direction: it prompts.
func TestStaticStoreWithoutAllowlistDeniesAll(t *testing.T) {
	s := NewStaticPermissionStore(ModePermissive)
	if s.IsMCPAllowed("github__create_issue") {
		t.Error("a store with no allowlist must not report any tool as allowlisted")
	}
}

// File-backed stores keep their live-reload behavior: an allowlist edit must
// take effect without restarting the agent.
func TestFileBackedStoreStillRereadsAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.yaml")
	if err := os.WriteFile(path, []byte("mode: permissive\nmcp_allow:\n  - first__*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadPermissionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsMCPAllowed("first__x") {
		t.Fatal("initial allowlist not honored")
	}
	if err := os.WriteFile(path, []byte("mode: permissive\nmcp_allow:\n  - second__*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.IsMCPAllowed("second__y") {
		t.Error("edited allowlist not picked up — file-backed re-read regressed")
	}
	if s.IsMCPAllowed("first__x") {
		t.Error("removed pattern still allowlisted — file-backed re-read regressed")
	}
}
