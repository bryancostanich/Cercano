package ui

import "testing"

func TestPrettifyPath_WorkspaceRelative(t *testing.T) {
	got := prettifyPath(
		"/Users/bryan/git_repos/Cercano/source/server/internal/server/server.go",
		"/Users/bryan/git_repos/Cercano",
		"/Users/bryan",
		100,
	)
	want := "source/server/internal/server/server.go"
	if got != want {
		t.Errorf("workspace-rel: got %q want %q", got, want)
	}
}

func TestPrettifyPath_HomeRelativeWhenOutsideWorkspace(t *testing.T) {
	got := prettifyPath(
		"/Users/bryan/dotfiles/.config/nvim/init.lua",
		"/Users/bryan/git_repos/Cercano",
		"/Users/bryan",
		100,
	)
	want := "~/dotfiles/.config/nvim/init.lua"
	if got != want {
		t.Errorf("home-rel: got %q want %q", got, want)
	}
}

func TestPrettifyPath_AbsolutePassThroughWhenNoMatch(t *testing.T) {
	got := prettifyPath(
		"/etc/hosts",
		"/Users/bryan/git_repos/Cercano",
		"/Users/bryan",
		100,
	)
	if got != "/etc/hosts" {
		t.Errorf("no-match: got %q want /etc/hosts", got)
	}
}

func TestPrettifyPath_SegmentElideWhenWorkspaceRelStillTooLong(t *testing.T) {
	got := prettifyPath(
		"/Users/bryan/git_repos/Cercano/source/server/internal/meridian/manager.go",
		"/Users/bryan",
		"/Users/bryan",
		30,
	)
	// After ~/-relative: "~/git_repos/Cercano/source/server/internal/meridian/manager.go"
	// That's 65 cols — segment-elision must kick in to fit budget 30.
	if got == "/Users/bryan/git_repos/Cercano/source/server/internal/meridian/manager.go" {
		t.Errorf("expected elision, got original: %q", got)
	}
	if len(got) > 30 {
		t.Errorf("over budget: %d cols, want ≤30: %q", len(got), got)
	}
	// Must end with the file basename so identity is preserved.
	if got[len(got)-len("manager.go"):] != "manager.go" {
		t.Errorf("basename lost: %q", got)
	}
}

func TestPrettifyPath_SegmentElidePreservesLeadingAndTrailing(t *testing.T) {
	// Workspace-rel'd path is "internal/meridian/manager.go" — already short
	// and fits. Force elision by passing zero cwd/home and tight budget.
	got := prettifyPath(
		"git_repos/bryancostanich/Cercano/source/server/internal/meridian/manager.go",
		"",
		"",
		40,
	)
	// Expect something like "git_repos/.../manager.go" or
	// "git_repos/.../meridian/manager.go" — leading + trailing preserved.
	if got[:len("git_repos/")] != "git_repos/" {
		t.Errorf("leading segment lost: %q", got)
	}
	if got[len(got)-len("manager.go"):] != "manager.go" {
		t.Errorf("trailing basename lost: %q", got)
	}
	if !contains(got, "...") {
		t.Errorf("expected '...' in elided result, got %q", got)
	}
}

func TestPrettifyPath_BudgetZeroReturnsUnelided(t *testing.T) {
	// Budget 0 = no width constraint; workspace/home rels still apply, but
	// no segment elision happens regardless of length.
	long := "/Users/bryan/git_repos/Cercano/source/server/internal/meridian/manager.go"
	got := prettifyPath(long, "/Users/bryan/git_repos/Cercano", "/Users/bryan", 0)
	want := "source/server/internal/meridian/manager.go"
	if got != want {
		t.Errorf("budget=0 workspace-rel: got %q want %q", got, want)
	}
	// Outside cwd → ~/... — still no elision at budget 0.
	got = prettifyPath(long, "/some/other/dir", "/Users/bryan", 0)
	want = "~/git_repos/Cercano/source/server/internal/meridian/manager.go"
	if got != want {
		t.Errorf("budget=0 home-rel: got %q want %q", got, want)
	}
}

func TestPrettifyPath_EmptyInputReturnsEmpty(t *testing.T) {
	if got := prettifyPath("", "/cwd", "/home", 80); got != "" {
		t.Errorf("empty in, got %q", got)
	}
}

func TestPrettifyPath_RelativePathPassesThrough(t *testing.T) {
	// Relative paths shouldn't get cwd/home stripped — they're already short
	// and we don't want false positives that mangle "source/foo.go" because
	// it happens to share a prefix with cwd.
	got := prettifyPath("source/server.go", "/Users/bryan/Cercano", "/Users/bryan", 100)
	if got != "source/server.go" {
		t.Errorf("got %q want unchanged", got)
	}
}

func TestSegmentElide_PreservesShortPaths(t *testing.T) {
	// 2 or fewer segments: nothing meaningful to elide.
	if got := segmentElide("foo", 80); got != "foo" {
		t.Errorf("1-seg: got %q", got)
	}
	if got := segmentElide("foo/bar", 80); got != "foo/bar" {
		t.Errorf("2-seg: got %q", got)
	}
}

// contains is a tiny helper to keep tests readable.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
