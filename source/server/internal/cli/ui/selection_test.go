package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/server/internal/cli/theme"
)

func TestSelectedTextSingleLine(t *testing.T) {
	m := Model{
		viewportPlainLines: []string{"hello world"},
		selection: textSelection{
			Active: true,
			Anchor: selectionPoint{
				Line: 0,
				Col:  6,
			},
			Cursor: selectionPoint{
				Line: 0,
				Col:  11,
			},
		},
	}

	if got, want := m.selectedText(), "world"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestSelectedTextMultilineReverseDrag(t *testing.T) {
	m := Model{
		viewportPlainLines: []string{"first line", "second", "third"},
		selection: textSelection{
			Active: true,
			Anchor: selectionPoint{
				Line: 2,
				Col:  2,
			},
			Cursor: selectionPoint{
				Line: 0,
				Col:  6,
			},
		},
	}

	if got, want := m.selectedText(), "line\nsecond\nth"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestSelectionPointFromMouseUsesViewportOffset(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	vp := viewport.New(viewport.WithWidth(20), viewport.WithHeight(4))
	vp.SetContent(strings.Join(lines, "\n"))
	vp.SetYOffset(10)

	m := Model{
		scrollbarTop:       2,
		viewport:           vp,
		viewportPlainLines: lines,
	}

	got := m.selectionPointFromMouse(tea.Mouse{X: 3, Y: 4}, false)
	want := selectionPoint{Line: 12, Col: 3}
	if got != want {
		t.Fatalf("selectionPointFromMouse() = %#v, want %#v", got, want)
	}
}

func TestRenderSelectionOnLinePreservesPlainText(t *testing.T) {
	p := theme.Cracker()
	m := Model{
		palette: p,
		viewport: viewport.New(
			viewport.WithWidth(10),
			viewport.WithHeight(3),
		),
		selection: textSelection{
			Active: true,
			Anchor: selectionPoint{Line: 0, Col: 2},
			Cursor: selectionPoint{Line: 0, Col: 5},
		},
	}

	line := lipgloss.NewStyle().Foreground(p.Primary).Render("0123456789")
	got := m.renderSelectionOnLine(line, 0)
	if stripped := lipgloss.Width(got); stripped != 10 {
		t.Fatalf("renderSelectionOnLine width = %d, want 10", stripped)
	}
}

func TestIsSelectionCopyKeyRecognizesCommandC(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{
			name: "super c",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "meta c compatibility",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModMeta},
			want: true,
		},
		{
			name: "keyboard protocol base code",
			msg:  tea.KeyPressMsg{Code: 'ç', BaseCode: 'c', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "plain c handled elsewhere",
			msg:  tea.KeyPressMsg{Code: 'c'},
			want: false,
		},
		{
			name: "alt c is not command",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSelectionCopyKey(tt.msg); got != tt.want {
				t.Fatalf("isSelectionCopyKey() = %v, want %v", got, tt.want)
			}
		})
	}
}
