package protocols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSkillFiles(t *testing.T) {
	root := t.TempDir()
	written, err := WriteSkillFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("no files written")
	}
	p := filepath.Join(root, ".agents", "skills", "design-decisions", "SKILL.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected %s: %v", p, err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "---\nname: design-decisions\n") {
		t.Fatal("missing/incorrect frontmatter")
	}
	if !strings.Contains(s, "Design Decision Protocol") {
		t.Fatal("body not written")
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "design-decisions", "SKILL.md")); err != nil {
		t.Fatalf(".claude mirror missing: %v", err)
	}
}
