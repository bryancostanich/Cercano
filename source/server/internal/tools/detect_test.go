package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  ProjectKind
	}{
		{"go", map[string]string{"go.mod": "module x\ngo 1.21\n"}, KindGo},
		{"fsproj", map[string]string{"App.fsproj": "<Project/>"}, KindDotnetProject},
		{"csproj", map[string]string{"App.csproj": "<Project/>"}, KindDotnetProject},
		{"sln plus fsproj", map[string]string{"App.sln": "", "src/App.fsproj": "<Project/>"}, KindDotnetSolution},
		{"cargo", map[string]string{"Cargo.toml": "[package]\nname='x'\n"}, KindRust},
		{"node with build", map[string]string{"package.json": `{"scripts":{"build":"webpack"}}`}, KindNode},
		{"node without build", map[string]string{"package.json": `{"scripts":{"test":"jest"}}`}, KindUnknown},
		{"empty", map[string]string{}, KindUnknown},
		{"rust beats go", map[string]string{"Cargo.toml": "", "go.mod": "module x"}, KindRust},
		{"go beats fsproj", map[string]string{"go.mod": "module x", "App.fsproj": "<Project/>"}, KindGo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tc.files)
			got, err := Detect(dir)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("Detect(%v) = %s, want %s", tc.files, got, tc.want)
			}
		})
	}
}
