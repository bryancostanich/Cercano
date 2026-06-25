package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"charm.land/bubbles/v2/viewport"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

var updateGolden = flag.Bool("update", false, "regenerate chatview golden files")

// newRenderModel builds a Model sized for rendering only (no agent needed for
// refreshViewport/renderViewportWithScrollbar). Host reserves 2 cols (gap +
// scrollbar), mirroring relayout's contentW-2.
//
// TASK 2 REWIRE: after the extraction the viewport + md move into chatView.
// Change this body to `m.chat = newChatView(m.styles, m.palette, width-2, height)`
// and delete the viewport/md lines. Do NOT pass -update — the goldens must still
// match byte-for-byte; that match is the whole point of this file.
func newRenderModel(width, height int) Model {
	m := Model{styles: theme.NewStyles(theme.Cracker()), palette: theme.Cracker(), focusedToolIdx: -1}
	m.viewport = viewport.New(viewport.WithWidth(width-2), viewport.WithHeight(height))
	m.md = render.NewMarkdown(theme.CrackerMarkdownStyle())
	return m
}

// renderFixture renders entries through the MAIN-PAGE path and compares to the
// golden file, regenerating it under -update. yOffset scrolls before render.
func renderFixture(t *testing.T, name string, entries []*Entry, width, height, yOffset int) {
	t.Helper()
	m := newRenderModel(width, height)
	m.entries = entries
	m.refreshViewport()
	m.viewport.SetYOffset(yOffset)
	got := m.renderViewportWithScrollbar()

	path := filepath.Join("testdata", "chatview", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update once to create)", path, err)
	}
	if got != string(want) {
		t.Errorf("render mismatch for %s (run -update to inspect diff)", name)
	}
}

func fixtureEntries() map[string][]*Entry {
	return map[string][]*Entry{
		"user_assistant_system": {
			{Role: RoleUser, Content: "how do I extract the transcript view"},
			{Role: RoleAssistant, Content: "You lift `renderEntry` into a `chatView`."},
			{Role: RoleSystem, Content: "done"},
		},
		"markdown_table_code": {
			{Role: RoleAssistant, Content: "Heading:\n\n# Title\n\nText with `code` and:\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```\n"},
		},
		"live_tail_open_fence": {
			{Role: RoleAssistant, Content: "Starting:\n\n```go\nfunc partial() {"},
		},
	}
}

func TestChatView_GoldenParity(t *testing.T) {
	for _, w := range []int{40, 120} {
		for name, entries := range fixtureEntries() {
			name, entries, w := name, entries, w
			t.Run(name+"_w"+itoa(w), func(t *testing.T) {
				renderFixture(t, name+"_w"+itoa(w), entries, w, 20, 0)
			})
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
