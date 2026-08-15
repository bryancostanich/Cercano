package theme

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestProductRenderCodeUsesThemeColors(t *testing.T) {
	root := filepath.Join("..")
	checks := []struct {
		name string
		re   *regexp.Regexp
	}{
		{name: "hex color literal", re: regexp.MustCompile(`#[0-9A-Fa-f]{6}`)},
		{name: "truecolor SGR literal", re: regexp.MustCompile(`(?:38|48);2`)},
		{name: "literal lipgloss color", re: regexp.MustCompile(`lipgloss\.Color\("`)},
		{name: "naked faint style", re: regexp.MustCompile(`Faint\(true\)`)},
		{name: "raw RGB endpoint", re: regexp.MustCompile(`\[3\]uint8`)},
	}
	for _, dir := range []string{"banner", "ui", "render"} {
		dir := filepath.Join(root, dir)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			s := bufio.NewScanner(file)
			line := 0
			for s.Scan() {
				line++
				text := s.Text()
				for _, check := range checks {
					if check.re.MatchString(text) {
						t.Errorf("%s:%d contains %s; product render colors must come from internal/theme: %s", path, line, check.name, strings.TrimSpace(text))
					}
				}
			}
			return s.Err()
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}
