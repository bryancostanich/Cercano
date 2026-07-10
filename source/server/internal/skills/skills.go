// Package skills is the single source of truth for Cercano's tool Agent
// Skills. The canonical SKILL.md files live in the embedded catalog/
// directory; the on-disk .agents/skills and .claude/skills trees at the repo
// root are GENERATED from it via WriteTrees (`cercano skills sync`). Do not
// edit those trees by hand — edit catalog/ and regenerate, or the drift-gate
// test (TestGeneratedTreesMatchRepo) will fail.
//
// Flavors: the .agents tree receives the canonical content verbatim. The
// .claude tree receives the canonical content plus a fixed "display the
// result" section inserted before the first H2 heading — Claude Code
// sessions don't always surface MCP tool results to the user, so the skill
// must instruct the model to echo them.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed catalog
var catalogFS embed.FS

// Skill is one entry in the canonical catalog.
type Skill struct {
	Name        string // directory name, e.g. "cercano-research"
	Description string // frontmatter description line (for RPC listing)
	Content     string // canonical SKILL.md content (the .agents flavor)
}

// displayResultSection is the .claude-flavor addition. Inserted before the
// first "## " heading of the canonical content.
const displayResultSection = "## Important: Display the result\n\n" +
	"MCP tool results may not be visible to the user in the terminal. " +
	"After calling the tool, you MUST output the full tool result text " +
	"verbatim in your response so the user can see it.\n\n"

// Catalog returns all skills in the embedded catalog, sorted by name.
func Catalog() ([]Skill, error) {
	entries, err := fs.ReadDir(catalogFS, "catalog")
	if err != nil {
		return nil, fmt.Errorf("skills: read embedded catalog: %w", err)
	}
	out := make([]Skill, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(catalogFS, "catalog/"+e.Name()+"/SKILL.md")
		if err != nil {
			return nil, fmt.Errorf("skills: %s has no SKILL.md: %w", e.Name(), err)
		}
		content := string(raw)
		desc, err := frontmatterDescription(content)
		if err != nil {
			return nil, fmt.Errorf("skills: %s: %w", e.Name(), err)
		}
		out = append(out, Skill{Name: e.Name(), Description: desc, Content: content})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// frontmatterDescription extracts the description value from the first
// frontmatter block. Fails loudly on malformed frontmatter: a skill without
// a description would render an empty RPC listing entry, which is silent
// breakage of exactly the kind this package exists to prevent.
func frontmatterDescription(content string) (string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", fmt.Errorf("missing frontmatter open")
	}
	for _, ln := range lines[1:] {
		if strings.TrimSpace(ln) == "---" {
			return "", fmt.Errorf("frontmatter has no description key")
		}
		if v, ok := strings.CutPrefix(ln, "description:"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", fmt.Errorf("frontmatter never closed")
}

// RenderClaude returns the .claude flavor of canonical content: the
// display-result section is inserted immediately before the first line that
// starts a "## " heading. Content with no H2 headings gets the section
// appended at the end.
func RenderClaude(content string) string {
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			head := strings.Join(lines[:i], "\n")
			tail := strings.Join(lines[i:], "\n")
			return head + "\n" + displayResultSection + tail
		}
	}
	return strings.TrimRight(content, "\n") + "\n\n" + strings.TrimRight(displayResultSection, "\n") + "\n"
}

// trees maps each on-disk skill tree to its renderer.
var trees = []struct {
	dir    string
	render func(string) string
}{
	{".agents/skills", func(c string) string { return c }},
	{".claude/skills", RenderClaude},
}

// WriteTrees renders every catalog skill into both discovery trees under
// rootDir, returning the paths written.
func WriteTrees(rootDir string) ([]string, error) {
	cat, err := Catalog()
	if err != nil {
		return nil, err
	}
	var written []string
	for _, sk := range cat {
		for _, t := range trees {
			dir := filepath.Join(rootDir, t.dir, sk.Name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return written, fmt.Errorf("skills: mkdir %s: %w", dir, err)
			}
			path := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(path, []byte(t.render(sk.Content)), 0o644); err != nil {
				return written, fmt.Errorf("skills: write %s: %w", path, err)
			}
			written = append(written, path)
		}
	}
	return written, nil
}
