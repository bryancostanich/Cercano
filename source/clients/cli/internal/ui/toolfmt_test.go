package ui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{686 * time.Millisecond, "686ms"},
		{1230 * time.Millisecond, "1.23s"},
		{500 * time.Microsecond, "<1ms"},
		{0, "<1ms"},
		{12 * time.Millisecond, "12ms"},
	}
	for _, c := range cases {
		if got := formatDur(c.d); got != c.want {
			t.Errorf("formatDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRelPath(t *testing.T) {
	root := "/home/u/proj"
	home := "/home/u"
	cases := []struct {
		p, want string
	}{
		{"/home/u/proj/a/b.go", "a/b.go"},
		{"/home/u/proj/README.md", "README.md"},
		{"/home/u/notes.txt", "~/notes.txt"},
		{"/etc/hosts", "/etc/hosts"},
		{"rel/x.go", "rel/x.go"},
		{"/home/u/proj", "."},
	}
	for _, c := range cases {
		if got := relPath(c.p, root, home); got != c.want {
			t.Errorf("relPath(%q) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestHumanizeArgs(t *testing.T) {
	root := "/home/u/proj"
	home := "/home/u"
	cases := []struct {
		name, tool, json, want string
	}{
		{"bash simple", "Bash", `{"cmd":["find","~","-iname","cercano"]}`, "find ~ -iname cercano"},
		{"bash quote spaces", "Bash", `{"cmd":["grep","-n","foo bar","file"]}`, "grep -n 'foo bar' file"},
		{"read path", "Read", `{"path":"/home/u/proj/README.md"}`, "README.md"},
		{"edit path", "Edit", `{"path":"/home/u/proj/internal/ui/model.go","old_string":"a","new_string":"b"}`, "internal/ui/model.go"},
		// The server now streams summarized args (big content fields truncated
		// with an ellipsis) — the path must still resolve from that valid JSON.
		{"edit path from summarized args", "Edit", `{"path":"/home/u/proj/internal/ui/model.go","old_string":"aaaa…","new_string":"bbbb…"}`, "internal/ui/model.go"},
		{"glob pattern", "Glob", `{"pattern":"README*"}`, "README*"},
		{"glob pattern in path", "Glob", `{"pattern":"*.go","path":"/home/u/proj/internal"}`, "*.go in internal"},
		{"grep pattern at root", "Grep", `{"pattern":"TODO","path":"/home/u/proj"}`, "TODO"},
		{"git commit message", "git_commit", `{"message":"fix: list color"}`, "fix: list color"},
		{"git push", "git_push", `{"remote":"origin","branch":"main"}`, "origin main"},
		{"unknown scalars sorted", "Frobnicate", `{"b":"two","a":"one"}`, "a=one b=two"},
		{"empty args", "git_status", `{}`, ""},
	}
	for _, c := range cases {
		if got := humanizeArgs(c.tool, c.json, root, home); got != c.want {
			t.Errorf("%s: humanizeArgs(%s, %s) = %q, want %q", c.name, c.tool, c.json, got, c.want)
		}
	}
}

func TestHumanizeResult(t *testing.T) {
	cases := []struct {
		name, detail, errSummary string
		isErr                    bool
		d                        time.Duration
		want                     string
	}{
		{"detail and timing", "exit 1", "", false, 686 * time.Millisecond, "exit 1 · 686ms"},
		{"rows detail", "12 rows", "", false, 4 * time.Millisecond, "12 rows · 4ms"},
		{"no detail timing only", "", "", false, time.Millisecond, "1ms"},
		{"zero matches detail", "0 matches", "", false, 2 * time.Millisecond, "0 matches · 2ms"},
		{"error uses summary", "", "permission denied", true, 5 * time.Millisecond, "permission denied · 5ms"},
		{"error trims long", "", strings.Repeat("x", 80), true, time.Millisecond, strings.Repeat("x", 60) + "… · 1ms"},
	}
	for _, c := range cases {
		if got := humanizeResult(c.detail, c.errSummary, c.isErr, c.d); got != c.want {
			t.Errorf("%s: humanizeResult(%q,%q,%v,%v) = %q, want %q", c.name, c.detail, c.errSummary, c.isErr, c.d, got, c.want)
		}
	}
}
