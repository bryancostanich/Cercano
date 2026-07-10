package protocols

import (
	"fmt"
	"os"
	"path/filepath"
)

// SkillContent renders a protocol as a canonical Agent Skills SKILL.md file.
// This is the shared renderer for both generated host-discovery files and the
// runtime RPC/MCP skill catalog, so the two surfaces cannot drift.
func SkillContent(p Protocol) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", p.Name, p.Description, p.Body)
}

// skillTrees are the on-disk skill directories hosts discover.
var skillTrees = []string{".agents/skills", ".claude/skills"}

// WriteSkillFiles renders every builtin protocol as a SKILL.md under each skill
// tree below rootDir, so host agents (Claude Code, etc.) discover the protocols
// natively. Frontmatter is generated from Name/Description; the body is the
// protocol Body. Returns the paths written.
func WriteSkillFiles(rootDir string) ([]string, error) {
	var written []string
	for _, p := range Builtins() {
		content := SkillContent(p)
		for _, tree := range skillTrees {
			dir := filepath.Join(rootDir, tree, p.Name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return written, fmt.Errorf("protocols: mkdir %s: %w", dir, err)
			}
			path := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return written, fmt.Errorf("protocols: write %s: %w", path, err)
			}
			written = append(written, path)
		}
	}
	return written, nil
}
