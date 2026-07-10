package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogParses(t *testing.T) {
	cat, err := Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(cat) == 0 {
		t.Fatal("catalog is empty")
	}
	for _, sk := range cat {
		if sk.Name == "" || sk.Description == "" || sk.Content == "" {
			t.Errorf("skill %q has empty fields (desc=%q, %d content bytes)", sk.Name, sk.Description, len(sk.Content))
		}
		if !strings.HasPrefix(sk.Name, "cercano-") {
			t.Errorf("skill %q: catalog entries must be cercano-* tool skills", sk.Name)
		}
	}
}

func TestRenderClaudeInsertsSectionBeforeFirstH2(t *testing.T) {
	in := "---\nname: x\ndescription: d\n---\n\n# Title\n\nIntro paragraph.\n\n## MCP Tool\n\nbody\n"
	out := RenderClaude(in)
	idx := strings.Index(out, "## Important: Display the result")
	h2 := strings.Index(out, "## MCP Tool")
	if idx < 0 {
		t.Fatal("display-result section not inserted")
	}
	if h2 < idx {
		t.Errorf("section must precede the first original H2 (got section@%d, h2@%d)", idx, h2)
	}
	if !strings.Contains(out, "Intro paragraph.") {
		t.Error("original intro lost")
	}
}

func TestRenderClaudeNoH2Appends(t *testing.T) {
	in := "---\nname: x\ndescription: d\n---\n\n# Title\n\nOnly intro.\n"
	out := RenderClaude(in)
	if !strings.Contains(out, "## Important: Display the result") {
		t.Fatal("section not appended for content without H2")
	}
}

// repoRoot walks up from the working directory until it finds the repo root
// (identified by .agents/skills existing). Returns "" when not inside the
// repo — e.g. when the module is vendored or built standalone.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, ".agents", "skills")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// TestGeneratedTreesMatchRepo is the drift gate: the checked-in .agents/skills
// and .claude/skills trees must be byte-identical to what WriteTrees renders
// from the embedded catalog. If this fails, someone edited a tree by hand —
// edit source/server/internal/skills/catalog/ instead and run
// `cercano skills sync`.
func TestGeneratedTreesMatchRepo(t *testing.T) {
	root := repoRoot(t)
	if root == "" {
		t.Skip("not inside the Cercano repo; drift gate only applies there")
	}
	tmp := t.TempDir()
	if _, err := WriteTrees(tmp); err != nil {
		t.Fatalf("WriteTrees: %v", err)
	}
	cat, err := Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	for _, sk := range cat {
		for _, tree := range []string{".agents/skills", ".claude/skills"} {
			rel := filepath.Join(tree, sk.Name, "SKILL.md")
			want, err := os.ReadFile(filepath.Join(tmp, rel))
			if err != nil {
				t.Fatalf("read generated %s: %v", rel, err)
			}
			got, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Errorf("repo missing %s — run `cercano skills sync`", rel)
				continue
			}
			if string(got) != string(want) {
				t.Errorf("DRIFT in %s: checked-in file differs from generated content — edit internal/skills/catalog/ and run `cercano skills sync`", rel)
			}
		}
	}
}
