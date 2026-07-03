package slash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeRepo creates a minimal directory tree satisfying the Cercano repo
// markers: source/server/cmd/cercano (dir) + source/clients/cli/main.go (file).
func makeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "source", "server", "cmd", "cercano"), 0o755); err != nil {
		t.Fatal(err)
	}
	cliDir := filepath.Join(root, "source", "clients", "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveDevRepoExplicit(t *testing.T) {
	repo := makeRepo(t)
	got, err := ResolveDevRepo(repo, t.TempDir(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestResolveDevRepoExplicitInvalid(t *testing.T) {
	if _, err := ResolveDevRepo(t.TempDir(), t.TempDir(), ""); err == nil {
		t.Fatal("want error for explicit path that is not a repo root")
	}
}

func TestResolveDevRepoWalkUp(t *testing.T) {
	repo := makeRepo(t)
	deep := filepath.Join(repo, "source", "clients", "cli") // depth 3 below root
	got, err := ResolveDevRepo("", deep, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestResolveDevRepoEnvFallback(t *testing.T) {
	repo := makeRepo(t)
	got, err := ResolveDevRepo("", t.TempDir(), repo) // cwd is NOT inside a repo
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestResolveDevRepoMiss(t *testing.T) {
	_, err := ResolveDevRepo("", t.TempDir(), "")
	if err == nil {
		t.Fatal("want error when nothing resolves")
	}
	// The error must name all three resolution paths so the user can fix it.
	for _, want := range []string{"/d <path>", "CERCANO_REPO"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing hint %q", err.Error(), want)
		}
	}
}

func TestDevCommandDispatch(t *testing.T) {
	repo := makeRepo(t)
	r := New()
	RegisterDev(r)
	res, ok := r.Dispatch("/d " + repo)
	if !ok {
		t.Fatal("dispatch did not match /d")
	}
	if res.Kind != ResultDevMode {
		t.Fatalf("kind = %v, want ResultDevMode", res.Kind)
	}
	if res.WorkDir != repo {
		t.Fatalf("WorkDir = %q, want %q", res.WorkDir, repo)
	}
}

func TestDevCommandDispatchAlias(t *testing.T) {
	repo := makeRepo(t)
	r := New()
	RegisterDev(r)
	res, ok := r.Dispatch("/dev " + repo)
	if !ok || res.Kind != ResultDevMode {
		t.Fatalf("alias /dev did not dispatch to dev mode: ok=%v kind=%v", ok, res.Kind)
	}
}

// TestResolveDevRepoEnvFallbackRelativeIsAbsolute asserts that a relative path
// in CERCANO_REPO is normalized to an absolute path (via filepath.Abs) before
// being returned, regardless of how the caller expressed it.
func TestResolveDevRepoEnvFallbackRelativeIsAbsolute(t *testing.T) {
	repo := makeRepo(t)
	// Change the process cwd to the repo's parent so that just the base name
	// is a valid relative path to the repo.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir) //nolint:errcheck
	if err := os.Chdir(filepath.Dir(repo)); err != nil {
		t.Skipf("chdir: %v", err)
	}
	rel := filepath.Base(repo) // relative path from current dir

	got, err := ResolveDevRepo("", t.TempDir(), rel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("want absolute path, got %q", got)
	}
	// Normalize symlinks (macOS /var → /private/var) before comparing.
	gotReal, _ := filepath.EvalSymlinks(got)
	repoReal, _ := filepath.EvalSymlinks(repo)
	if gotReal != repoReal {
		t.Fatalf("got %q (real %q), want %q (real %q)", got, gotReal, repo, repoReal)
	}
}

func TestDevKickoffNamesTheDocs(t *testing.T) {
	kick := DevKickoff("/tmp/x")
	for _, want := range []string{
		"/tmp/x",
		"docs/features/cli/README.md",
		"docs/agent/README.md",
		"docs/agent/self-dev.md",
	} {
		if !strings.Contains(kick, want) {
			t.Fatalf("kickoff missing %q", want)
		}
	}
}
