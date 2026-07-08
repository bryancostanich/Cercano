package builtins

import "testing"

func TestResolvePath(t *testing.T) {
	cases := []struct{ name, workDir, p, want string }{
		{"relative joins workdir", "/repo", "internal/x.go", "/repo/internal/x.go"},
		{"absolute unchanged", "/repo", "/etc/hosts", "/etc/hosts"},
		{"empty workdir unchanged", "", "internal/x.go", "internal/x.go"},
		{"empty path unchanged", "/repo", "", ""},
		{"dot resolves to workdir", "/repo", ".", "/repo"},
	}
	for _, c := range cases {
		if got := resolvePath(c.workDir, c.p); got != c.want {
			t.Errorf("%s: resolvePath(%q,%q)=%q want %q", c.name, c.workDir, c.p, got, c.want)
		}
	}
}
