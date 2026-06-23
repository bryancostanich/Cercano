# Cercano TUI → Charm v2 Migration

## Goal

Upgrade the cercano-cli TUI from Charm **v1 → v2** (bubbletea, bubbles, lipgloss),
idiomatically, and adopt four new v2 capabilities:

- Real terminal cursor at the input box
- Enhanced keyboard mode
- Mouse-wheel scrolling of the chat history
- Declarative screen control (alt-screen / clear handled via the View struct)

Charm released v2 of all three libraries together on 2026-02-23 (first breaking
changes in the project's history).

## Module Paths

| v1 | v2 |
|----|----|
| `github.com/charmbracelet/bubbletea v1.3.10` | `charm.land/bubbletea/v2` |
| `github.com/charmbracelet/bubbles v1.0.0` | `charm.land/bubbles/v2` |
| `github.com/charmbracelet/lipgloss v1.1.0` | `charm.land/lipgloss/v2` |

All three upgrade together. The `charm.land` paths come straight from the v2
upgrade guides; confirm resolution with `go get charm.land/bubbletea/v2@latest`.
If the vanity path balks, fall back to `github.com/charmbracelet/<lib>/v2`.

## Surface

Twelve files import the Charm TUI libs, in three buckets:

**Root model** (the bulk of the work):
- `internal/cli/ui/model.go` (1711 lines)

**Sub-models** — plain structs with their own Update/View, called manually by
the root (NOT registered with the Program):
- `internal/cli/ui/config_editor.go`
- `internal/cli/ui/history_picker.go`
- `internal/cli/banner/anim.go`
- `internal/cli/overlay/rowlist.go`

**Lipgloss-only render/style:**
- `internal/cli/theme/styles.go`
- `internal/cli/theme/palette.go`
- `internal/cli/banner/banner.go`
- `internal/cli/render/table.go`
- `internal/cli/ui/scrollback_tool.go`

**Entry point + test:**
- `cmd/cercano/main.go` (`tea.NewProgram`)
- `internal/cli/overlay/rowlist_test.go` (constructs `tea.KeyMsg` literals)

## Breaking Changes and Mapping

### 1. Imports
All 12 files plus `go.mod` move to the v2 module paths.

### 2. `tea.KeyMsg` → `tea.KeyPressMsg`
In v2, `tea.KeyMsg` is an interface covering press + release. Use
`tea.KeyPressMsg` in the type switch.

- `msg.String()`-based matching (ctrl+c, tab, enter, pgup, shift+up, …) is
  **unchanged** — `String()` still returns the same tokens (note `" "` →
  `"space"`, which this code does not rely on).
- The `msg.Type` switch over `tea.KeyUp/KeyDown/KeyEnter/KeyTab/KeyEsc`
  (model.go tool-nav block) — `msg.Type` is **removed** in v2. Replace with
  `key.Matches` against a binding set (the idiomatic path).

### 3. `View() string` → `View() tea.View`
Only the **root** `ui.Model` is registered with the Program, so only its
`View()` changes signature. It returns `tea.NewView(joined)` and then sets the
declarative fields (Cursor, AltScreen, MouseMode, KeyboardEnhancements).

Sub-models (`configEditor`, `historyPicker`, `AnimModel`, `RowList`) keep
`View() string` — the root joins their strings as it does today.

### 4. lipgloss
- No `AdaptiveColor` and no `DefaultRenderer()` usage in this codebase → the
  color/style API is source-compatible. `lipgloss.Color`, `lipgloss.Width`,
  `JoinHorizontal`, `Border`, `NewStyle().Foreground(...)` all carry over.
- Color downsampling now lives inside bubbletea v2; because everything renders
  through the `tea` program, no standalone `colorprofile` writer is needed.

### 5. bubbles
- `textinput`: style fields relocate — `PromptStyle`→`Styles.Prompt`,
  `TextStyle`→`Styles.Text`, `PlaceholderStyle`→`Styles.Placeholder`,
  `CursorStyle`→`Styles.Cursor`; the `Cursor` field becomes a `Cursor()` method
  returning `*tea.Cursor`. `textinput.Blink` still exists.
- `viewport`: `Width`/`Height` fields → `SetWidth(w)`/`SetHeight(h)` setters and
  `Width()`/`Height()` getters. Touches `relayout()` and `renderEntry()` wrap-width
  reads in model.go.

## New Features

- **A. Real terminal cursor** — root `View.Cursor` is set from `m.input.Cursor()`
  when the input owns focus, so the terminal draws a true blinking cursor at the
  input column instead of a simulated caret.
- **B. Enhanced keyboard** — requested via `View.KeyboardEnhancements`, making the
  shift+up/down (and any modifier) keys the scrollback-nav code already routes
  reliable across terminals.
- **C. Mouse wheel** — `View.MouseMode = MouseModeCellMotion`; wheel messages are
  forwarded to the chat viewport (bubbles v2 viewport consumes them).
- **D. Declarative screen** — `View.AltScreen = true` replaces
  `tea.WithAltScreen()`. The manual `tea.ClearScreen`-on-resize and the
  `View()` height-padding loop are removed **only if** v2's renderer makes them
  unnecessary; that single sub-change is validated in Phase 3 and reverts on its
  own if a redraw regression appears.

## Idiomatic Refactor

- New `internal/cli/ui/keys.go`: a `keyMap` struct of `key.Binding` built once;
  `Update` uses `key.Matches` for nav/scroll keys. ctrl+c arming and slash-command
  completion stay string-based where that reads clearer.
- `internal/cli/overlay/rowlist.go` `Update`: same treatment (up/k, down/j,
  home/g, end/G, enter, esc/q).

## Sequencing

Each phase compiles and keeps the unit tests green.

- **Phase 0 — Setup.** Worktree (done: `tui-charm-v2`). Bump deps: `go get`
  all three v2 modules, `go mod tidy`.
- **Phase 1 — Mechanical port.** Imports, `KeyPressMsg`, `View()→tea.View`
  (still string-match keys, still `WithAltScreen`), viewport/textinput
  field→method changes, fix `rowlist_test`. Behavior identical to v1.
- **Phase 2 — Idiomatic keys.** `keys.go` + `key.Matches` in model and rowlist.
- **Phase 3 — New features.** Cursor, enhanced keyboard, mouse wheel, declarative
  screen; delete now-dead hacks.
- **Phase 4 — Smoke test.** Manual run in a real terminal.

## Testing

- **Unit:** existing tests stay green — `rowlist_test`, `confirm_test`,
  `banner_test`, `table_test`, `markdown_test`, `scrollback_tool_test`.
  `rowlist_test` key construction updates from `tea.KeyMsg{Type: ...}` to the v2
  `tea.KeyPressMsg{...}` form. `confirm_test` is insulated — it calls
  `resolveConfirmKey(string)` directly.
- **Manual smoke checklist** (TUI can't be fully unit-tested): splash render,
  type + submit, slash-command completion, `/config` and `/history` overlays,
  tool confirm y/n/d, ctrl+c arm-then-quit, terminal resize, mouse-wheel scroll,
  streaming spinner/sweep animation, input cursor blink.

## Risks

- **`charm.land` vanity path** — resolve check at Phase 0; fall back to
  `github.com/charmbracelet/<lib>/v2` if needed.
- **Declarative alt-screen regressing resize-clear** — the manual hacks exist to
  fix stale-row artifacts on shrink. Isolated to one sub-change in Phase 3 and
  revertable without affecting the rest of the migration.
- **textinput style relocation** — the input prompt is currently styled via
  `ti.Prompt = s.UserPrompt.Render("▶ ")` (a pre-rendered string, not a style
  field), so it is unaffected; only verify the overlay edit-input prompt.
