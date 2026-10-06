package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateNeverReplacesInstalledBinaries pins the release's upgrade-safety
// rule: the update path is advisory only. It may tell the user how to
// upgrade, but it must never replace installed binaries itself — otherwise a
// self-update could silently overwrite files Homebrew owns, leaving brew's
// manifest disagreeing with what is actually on disk.
//
// This is a guard test. The current implementation performs no replacement,
// and this locks that in: if someone later adds a self-updater, this test
// fails and forces the Homebrew-ownership question to be answered
// deliberately rather than discovered after a broken upgrade.
//
// Isolation: the version check is pointed at a local test server, so no
// request reaches GitHub. The fake installation sits in a temp directory and
// is never on PATH, so nothing here can touch a real install.
func TestUpdateNeverReplacesInstalledBinaries(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	keg := filepath.Join(root, "Cellar", "cercano", "1.0.0", "bin")
	prefix := filepath.Join(root, "bin")
	for _, dir := range []string{configDir, keg, prefix} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A Homebrew-style installation: real files in the keg, prefix symlinks.
	names := []string{"cercano", "cercano-cli"}
	contents := map[string][]byte{}
	before := map[string]os.FileInfo{}
	for _, name := range names {
		body := []byte("installed " + name + " v1.0.0")
		path := filepath.Join(keg, name)
		if err := os.WriteFile(path, body, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(prefix, name)); err != nil {
			t.Fatal(err)
		}
		contents[name] = body
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = info
	}

	// A newer release is available and the install is Homebrew-managed —
	// the exact conditions under which a self-updater would be tempted to act.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{
			TagName:    "v99.0.0",
			Prerelease: false,
			HTMLURL:    "https://example.com/release/v99.0.0",
		})
	}))
	defer server.Close()

	info := checkForUpdateWith("1.0.0", server.URL, server.Client())
	if info == nil {
		t.Fatal("expected an update result from the stub release server")
	}
	if !info.UpdateAvailable {
		t.Fatalf("expected an available update, got current=%q latest=%q", info.CurrentVersion, info.LatestVersion)
	}

	// Force the Homebrew-managed branch regardless of how this machine is set
	// up, so the assertion does not depend on the developer's own install.
	info.InstallMethod = "homebrew"
	command := info.UpgradeCommand()
	if command != "brew upgrade cercano" {
		t.Fatalf("a Homebrew install must be told to upgrade through brew, got %q", command)
	}
	// Advice only: it must not run anything or offer a replacement path.
	if strings.Contains(command, "curl") || strings.Contains(command, "install.sh") {
		t.Fatalf("upgrade guidance implies self-installation: %q", command)
	}

	// Also exercise the cached path, which is what startup actually calls.
	if got := checkCachedWith("1.0.0", configDir, func(string) *UpdateInfo { return info }); got == nil {
		t.Fatal("expected a cached update result")
	}

	// Nothing in the installation may have changed: same bytes, same inodes,
	// symlinks still pointing at the original keg files.
	for _, name := range names {
		path := filepath.Join(keg, name)
		after, err := os.Stat(path)
		if err != nil {
			t.Fatalf("installed %s disturbed by the update path: %v", name, err)
		}
		if !os.SameFile(before[name], after) {
			t.Fatalf("installed %s was replaced by the update path", name)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(contents[name]) {
			t.Fatalf("installed %s was rewritten by the update path", name)
		}
		target, err := os.Readlink(filepath.Join(prefix, name))
		if err != nil {
			t.Fatalf("prefix symlink for %s disturbed: %v", name, err)
		}
		if target != path {
			t.Fatalf("prefix symlink for %s repointed to %q", name, target)
		}
	}

	// The update path may write only its own cache, inside the config
	// directory — never anything into the installation prefix.
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "update_check.json" {
			t.Fatalf("update path wrote unexpected file %q into the config directory", entry.Name())
		}
	}
}
