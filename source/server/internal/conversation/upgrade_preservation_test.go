package conversation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpgradePreservesUserData proves the data-safety half of the release's
// non-disruptive upgrade contract: replacing the installed binaries (what
// `brew upgrade cercano` does) must not disturb user state.
//
// The contract rests on user data living outside the install prefix. This
// test asserts that directly rather than assuming it:
//
//  1. Seed a real conversation database at the production-resolved location
//     under an isolated HOME, with a conversation and turns.
//  2. Replace every file in a Homebrew-style keg — new inodes, exactly like
//     a fresh bottle unpack — and delete the old keg entirely.
//  3. Reopen the database through the production resolver and assert the
//     conversation, its turns, and the config file survive byte-for-byte.
//
// Isolation: HOME points at a temp directory for the whole test, so the
// production DefaultPath resolver writes only there. CERCANO_CONVERSATIONS_DB
// is explicitly cleared so an ambient developer override cannot redirect this
// test onto the real database. Nothing under the developer's real
// ~/.config/cercano is opened, read, or written, and no agent is started.
func TestUpgradePreservesUserData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// Point the production resolver at the isolated HOME, and make sure no
	// inherited override can aim it at the real database.
	t.Setenv("HOME", home)
	t.Setenv("CERCANO_CONVERSATIONS_DB", "")

	dbPath, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	// The resolver must stay inside the isolated HOME; otherwise this test
	// would be operating on real user data. The expected path is hidden
	// (~/.config/...), so escape is detected via a ".." traversal, not a
	// leading dot.
	rel, err := filepath.Rel(home, dbPath)
	if err != nil || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("resolved database %q escaped the isolated HOME %q (rel=%q, err=%v)", dbPath, home, rel, err)
	}
	// Config lives beside the database, in the same user-state directory.
	configPath := filepath.Join(filepath.Dir(dbPath), "config.yaml")
	configBody := []byte("cloud_provider: anthropic\ncloud_model: test-model\n")
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed real user state through the production store.
	const convID = "upgrade-conv"
	const projectDir = "/tmp/project"
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureConversation(ctx, convID, projectDir, "test-model"); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []Turn{
		{ConversationID: convID, Role: "user", Content: "does my history survive an upgrade?"},
		{ConversationID: convID, Role: "assistant", Content: "it must."},
	} {
		if err := store.Append(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	// Close before replacement: an upgrade replaces binaries, and the old
	// process releases its handle when it exits.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Build the "old" installed keg, then replace it the way a bottle
	// upgrade does: brand-new files at a new version path, old keg removed,
	// prefix symlinks repointed.
	prefix := filepath.Join(root, "prefix", "bin")
	oldKeg := filepath.Join(root, "prefix", "Cellar", "cercano", "1.0.0", "bin")
	newKeg := filepath.Join(root, "prefix", "Cellar", "cercano", "2.0.0", "bin")
	for _, dir := range []string{prefix, oldKeg, newKeg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"cercano", "cercano-cli"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(oldKeg, name), []byte("old "+name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(oldKeg, name), filepath.Join(prefix, name)); err != nil {
			t.Fatal(err)
		}
	}
	oldInodes := map[string]os.FileInfo{}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(oldKeg, name))
		if err != nil {
			t.Fatal(err)
		}
		oldInodes[name] = info
	}
	// The upgrade itself.
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(newKeg, name), []byte("new "+name), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(prefix, name)
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(newKeg, name), link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(oldKeg); err != nil {
		t.Fatal(err)
	}

	// Confirm the replacement was real: different files, old keg gone.
	for _, name := range names {
		newInfo, err := os.Stat(filepath.Join(prefix, name))
		if err != nil {
			t.Fatalf("upgraded %s: %v", name, err)
		}
		if os.SameFile(oldInodes[name], newInfo) {
			t.Fatalf("%s was not actually replaced", name)
		}
	}
	if _, err := os.Stat(oldKeg); !os.IsNotExist(err) {
		t.Fatalf("old keg still present: %v", err)
	}

	// The upgraded binaries must resolve to the same user state.
	upgradedPath, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if upgradedPath != dbPath {
		t.Fatalf("database moved across upgrade: %q then %q", dbPath, upgradedPath)
	}
	reopened, err := Open(upgradedPath)
	if err != nil {
		t.Fatalf("reopen conversations after upgrade: %v", err)
	}
	defer reopened.Close()

	info, err := reopened.Get(ctx, convID)
	if err != nil {
		t.Fatalf("conversation lost across upgrade: %v", err)
	}
	if info.TurnCount != 2 {
		t.Fatalf("turn count after upgrade: want 2, got %d", info.TurnCount)
	}
	turns, err := reopened.GetTurns(ctx, convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns after upgrade: want 2, got %d", len(turns))
	}
	if turns[0].Content != "does my history survive an upgrade?" || turns[1].Content != "it must." {
		t.Fatalf("turn contents altered across upgrade: %q / %q", turns[0].Content, turns[1].Content)
	}
	// The conversation must remain listed for its project, so the upgraded
	// client still offers it for resume.
	listed, err := reopened.List(ctx, projectDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range listed {
		if candidate.ID == convID {
			found = true
		}
	}
	if !found {
		t.Fatal("conversation missing from its project listing after upgrade")
	}
	// Configuration must survive untouched.
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config lost across upgrade: %v", err)
	}
	if string(gotConfig) != string(configBody) {
		t.Fatalf("config altered across upgrade: %q", gotConfig)
	}
}
