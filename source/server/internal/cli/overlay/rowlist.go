// Package overlay holds reusable floating-panel UI components.
// RowList is the workhorse — a navigable list of key:value rows with optional
// inline edit. Used by /config (edit-on-Enter), and reusable for /history
// (pick-and-resume), /models (pick-and-switch), /mcp (pick-and-act), /font
// (pick-and-apply), /theme (pick-and-switch), etc.
package overlay

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cercano/source/server/internal/cli/theme"
)

// Row is one displayed line.
//
//   - Editable rows: Enter switches the row into inline edit mode; the value
//     can be typed; another Enter saves via Hooks.OnEdit; Esc cancels.
//   - Select rows (Editable=false, ReadOnly=false): Enter calls Hooks.OnSelect.
//     Use for pick-and-act flows.
//   - ReadOnly rows: navigable but Enter does nothing.
//   - Masked rows start blank on edit (used for secrets so the old value isn't
//     visible while typing).
type Row struct {
	Key      string // opaque to the overlay; forwarded to hooks
	Label    string
	Value    string
	Hint     string // shown dimly after value (e.g. "(read-only)", "current")
	Editable bool
	ReadOnly bool
	Masked   bool
}

// Hooks bundle the caller's row-action behavior. All fields optional.
type Hooks struct {
	// OnEdit fires when an Editable row's inline edit is committed with Enter.
	// Return the message to surface in the footer. Error converts to footer text
	// prefixed with "save failed:" and the row keeps its prior value.
	OnEdit func(row Row, newValue string) (status string, err error)

	// OnSelect fires when Enter is pressed on a non-Editable, non-ReadOnly row.
	// Return a footer status, whether to close the overlay, and an optional Cmd.
	OnSelect func(row Row) (status string, close bool, cmd tea.Cmd)

	// OnReload returns the latest row snapshot; called after a successful
	// OnEdit save so the overlay reflects the agent's new state. Optional.
	OnReload func() []Row
}

// RowList is the navigable overlay.
type RowList struct {
	Title  string
	Rows   []Row
	Hooks  Hooks
	cursor int

	editing bool
	input   textinput.Model
	status  string
}

// New constructs a RowList.
func New(title string, rows []Row, hooks Hooks) RowList {
	return RowList{Title: title, Rows: rows, Hooks: hooks}
}

// Cursor returns the currently-selected row index.
func (r RowList) Cursor() int { return r.cursor }

// SetStatus overrides the footer status (e.g. when the caller wants to seed
// an initial hint).
func (r *RowList) SetStatus(s string) { r.status = s }

// Update routes a key event. Returns (next, cmd, closed) — `closed` tells the
// host whether the overlay should be dismissed.
func (r RowList) Update(msg tea.KeyMsg, styles theme.Styles) (RowList, tea.Cmd, bool) {
	key := msg.String()
	if r.editing {
		switch key {
		case "esc":
			r.editing = false
			r.status = "canceled"
			return r, nil, false
		case "enter":
			val := r.input.Value()
			row := r.Rows[r.cursor]
			r.editing = false
			if r.Hooks.OnEdit == nil {
				r.status = "no edit handler"
				return r, nil, false
			}
			status, err := r.Hooks.OnEdit(row, val)
			if err != nil {
				r.status = "save failed: " + err.Error()
				return r, nil, false
			}
			r.status = status
			if r.Hooks.OnReload != nil {
				r.Rows = r.Hooks.OnReload()
				if r.cursor >= len(r.Rows) {
					r.cursor = len(r.Rows) - 1
				}
			}
			return r, nil, false
		}
		var cmd tea.Cmd
		r.input, cmd = r.input.Update(msg)
		return r, cmd, false
	}
	switch key {
	case "esc", "q":
		return r, nil, true
	case "up", "k":
		if r.cursor > 0 {
			r.cursor--
		}
	case "down", "j":
		if r.cursor < len(r.Rows)-1 {
			r.cursor++
		}
	case "home", "g":
		r.cursor = 0
	case "end", "G":
		r.cursor = len(r.Rows) - 1
	case "enter":
		row := r.Rows[r.cursor]
		switch {
		case row.ReadOnly:
			r.status = "read-only"
			return r, nil, false
		case row.Editable:
			ti := textinput.New()
			ti.Prompt = styles.Accent.Render("▸ ")
			ti.CharLimit = 0
			ti.Focus()
			if !row.Masked {
				ti.SetValue(row.Value)
				ti.CursorEnd()
			}
			r.input = ti
			r.editing = true
			r.status = ""
			return r, textinput.Blink, false
		default:
			if r.Hooks.OnSelect == nil {
				r.status = "no select handler"
				return r, nil, false
			}
			status, close, cmd := r.Hooks.OnSelect(row)
			r.status = status
			return r, cmd, close
		}
	}
	return r, nil, false
}

// View renders the panel. `width` is the terminal width; the panel sizes
// itself with 2-col outer margin.
func (r RowList) View(width int, palette theme.Palette, styles theme.Styles) string {
	innerW := width - 6
	if innerW < 40 {
		innerW = 40
	}

	labelW := 0
	for _, row := range r.Rows {
		if lw := lipgloss.Width(row.Label); lw > labelW {
			labelW = lw
		}
	}

	var body strings.Builder
	body.WriteString("\n")
	for i, row := range r.Rows {
		var marker string
		if i == r.cursor {
			marker = styles.Accent.Render(" ▶ ")
		} else {
			marker = "   "
		}
		label := styles.Muted.Render(row.Label)
		if i == r.cursor && !row.ReadOnly && !r.editing {
			label = styles.Bright.Render(row.Label)
		}
		pad := strings.Repeat(" ", labelW-lipgloss.Width(row.Label)+2)

		var valueCell string
		switch {
		case r.editing && i == r.cursor:
			valueCell = r.input.View()
		case row.Value == "":
			valueCell = styles.Dim.Render("(unset)")
		default:
			valueCell = styles.Primary.Render(row.Value)
		}
		if row.Hint != "" {
			valueCell += styles.BorderDim.Render("  " + row.Hint)
		}
		body.WriteString(marker)
		body.WriteString(label)
		body.WriteString(pad)
		body.WriteString(valueCell)
		body.WriteString("\n")
	}

	footer := styles.Muted.Render("   ↑↓ navigate  enter " + r.actionHint() + "  esc close")
	if r.editing {
		footer = styles.Muted.Render("   editing — enter saves, esc cancels")
	}
	if r.status != "" {
		footer += styles.BorderDim.Render("   ·   ") + styles.Accent.Render(r.status)
	}
	body.WriteString("\n" + footer + "\n")

	title := styles.Accent.Render("─ " + r.Title + " ")
	titleLine := title + styles.BorderDim.Render(strings.Repeat("─", innerW-lipgloss.Width(title)))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(palette.Border).
		Padding(0, 1).
		Width(innerW).
		Render(titleLine + "\n" + body.String())
}

func (r RowList) actionHint() string {
	if r.cursor >= len(r.Rows) {
		return "select"
	}
	row := r.Rows[r.cursor]
	switch {
	case row.ReadOnly:
		return "—"
	case row.Editable:
		return "edit"
	default:
		return "select"
	}
}
