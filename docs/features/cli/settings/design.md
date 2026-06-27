# Settings Page

## Overview / Goal

A richer, sectioned **settings page** for the cercano-cli TUI, in the same league as the model runtime dashboard (`/m`) and the context viewer (`/c`). It **replaces** the current flat `/config` editor.

Today's `/config` is a `configEditor` content page: a single flat `overlay.RowList` of config keys with inline text edit (`config_editor.go`). Other settings live in separate commands — permission mode (`/strict` `/permissive` `/bypass`), prompt accent color (`/color`), cloud shortcut (`/cloud`). There is no one organized surface to see and change everything.

This feature delivers that surface, and to do it well introduces a small **extensible form-field widget layer** (`internal/form`) so settings can use the right control per field (text, masked, select, toggle, read-only) and so new field types are cheap to add later.

**What changes:**
- New content page `settingsPage` (`contentPageID "settings"`) replaces `configEditor`.
- New `internal/form` package: a `Field` interface + concrete widgets + a `Form` that groups fields into titled sections.
- `/s`, `/settings`, and `/config` (alias) all open the settings page via a renamed result kind `ResultOpenSettings`.
- Permission mode, prompt accent color, and locus-mode are folded into the page (their standalone slash commands remain as shortcuts).

**What does NOT change:**
- The `GetConfig` / `UpdateConfig` / `GetPermissionMode` / `SetPermissionMode` RPCs (reused as-is).
- `overlay.RowList` (kept for pick-and-act overlays: `/history`, `/models`).
- The standalone `/strict` `/permissive` `/bypass` `/color` `/cloud` `/locus` commands (kept as shortcuts; the page is an additional, unified surface).
- The `/config key value` non-interactive direct-set form (kept).

## Design / Approach

### Design principles (load-bearing)

1. **Algorithmic over LLM.** No model is involved anywhere in this feature — field dispatch, value validation, section layout, and command routing are all deterministic.
2. **Clean CLI / agent separation.** The settings page renders and edits; it never mutates root model state directly. Cross-page effects (accent color, permission-mode chip) are emitted as `tea.Cmd` → custom messages the root model applies, mirroring the existing `ResultSetPromptColor` / `ResultSetPermissionMode` handling. Persisted state still flows through agent RPCs.
3. **Isolation and extensibility.** Each widget is a self-contained unit implementing one interface, testable on its own. Adding a new field type is a new file, not an edit to a growing switch.

### New `internal/form` package

The current `overlay.Row` (`Editable` / `ReadOnly` / `Masked` booleans) only supports inline text edit and select-callback — it cannot grow to pickers/toggles/future widgets without becoming a tangle. Instead, a dedicated widget layer:

```go
// Field is one interactive (or read-only) setting widget.
type Field interface {
    Key() string                       // opaque id, forwarded to the commit hook
    Label() string
    Display() string                   // value shown when the field is not being edited
    Editing() bool                     // true while the widget owns keystrokes (inline edit / open picker)
    // Update routes a key event. committed=true means the user accepted a new
    // value (newValue carries it); the Form then calls the field's commit hook.
    Update(msg tea.KeyPressMsg) (cmd tea.Cmd, committed bool, newValue string)
    View(focused bool, width int, s theme.Styles) string
}
```

Concrete widgets, one file each:

| Widget | Behavior |
|---|---|
| `TextField` | Inline `textinput`; enter→edit, enter saves, esc cancels. URLs, model names. |
| `MaskedField` | Like `TextField` but blank-on-edit; value shown as `(set)`/`(unset)`. Secrets. |
| `SelectField` | Holds an option list; enter opens an inline picker, ↑↓ choose, enter commit, esc cancel. Enums. |
| `ToggleField` | Boolean; space/enter flips. (Not used by V1 sections but part of the kit.) |
| `ReadOnlyField` | Display only; navigable, enter is a no-op. |

Future widgets (`NumberField`, `ColorField`, `PathField`, …) are added by implementing `Field` in a new file — no changes to `Form`.

`Form` arranges fields under named **sections**:

```go
type Section struct {
    Title  string
    Fields []Field
}

type Form struct {
    Sections []Section
    // cursor across the flattened focusable fields; section titles are
    // non-focusable headers.
    // commit hook fires on a field's committed=true:
    OnCommit func(key, value string) (status string, cmd tea.Cmd, err error)
}
```

`Form` owns ↑↓ navigation across the flattened field list, delegates key events to the focused field while it is `Editing()`, renders each section as a titled panel (same visual treatment as the `/m` dashboard blocks via the existing block-render helpers), and shows a footer with nav hints + last status.

### Settings page (`internal/ui/settings_page.go`)

Implements the `contentPage` interface (`ID`, `SetSize`, `Update`, `View`) and `contentPageScroller` for scroll support, like `runtimeDashboard` / `contextView`. It:

1. On open, snapshots state from `GetConfig` + `GetPermissionMode` + the root model's local theme state (accent color), and builds the `Form`.
2. Routes keys to the `Form`.
3. Provides the `Form.OnCommit` hook that dispatches each committed field to its sink.

### Sections & fields (V1)

| Section | Field | Widget | Sink |
|---|---|---|---|
| Local Model | local-runtime | Select (`ollama` \| `llama_server`) | UpdateConfig |
| Local Model | local-model | Text | UpdateConfig |
| Local Model | ollama-url | Text | UpdateConfig |
| Local Model | embedding-model | ReadOnly | — |
| Cloud | cloud-provider | Select (`anthropic` \| `google`) | UpdateConfig |
| Cloud | cloud-model | Text | UpdateConfig |
| Cloud | cloud-base-url | Text | UpdateConfig |
| Cloud | cloud-api-key | Masked | UpdateConfig |
| Cloud | cloud-state | ReadOnly | — |
| Routing | locus-mode | Select (`cloud_only` \| `cloud_primary` \| `local_primary` \| `local_only`) | UpdateConfig |
| Permissions | permission-mode | Select (`strict` \| `permissive` \| `bypass`) | SetPermissionMode RPC + emit chip-update msg |
| UI / Theme | accent-color | Select (palette names) + free-text hex escape hatch | emit color-changed msg (CLI-local) |
| Server | port | ReadOnly | — |

The accent-color field is a `SelectField` over the named palette colors, with a free-text hex (`#RRGGBB`) entry as the last option / escape hatch — not a dedicated `ColorField` in V1.

### Data flow / commit sinks

`Form.OnCommit(key, value)` routes by field key:

- **Config fields** → `ag.UpdateConfig(ctx, single-field update)` — same field mapping as today's `saveSingle`.
- **permission-mode** → `ag.SetPermissionMode(ctx, value)`, then return a `tea.Cmd` emitting `settingsPermissionModeChangedMsg{mode}` so the root model flips the status-bar permission chip (mirrors existing `ResultSetPermissionMode` handling).
- **accent-color** → return a `tea.Cmd` emitting `settingsColorChangedMsg{color}` so the root model updates `promptBorderColor` (CLI-local; mirrors `ResultSetPromptColor`).

After a successful config commit the page re-snapshots (re-reads `GetConfig`) so read-only/derived fields (`cloud-state`, etc.) reflect the change.

### Command binding

`/s`, `/settings`, and `/config` (alias) all dispatch `ResultOpenSettings`. The bare command opens the page. `/config key value` keeps its non-interactive direct-set behavior. `contentPageConfig` is renamed `contentPageSettings`; `ResultOpenConfigEditor` is renamed `ResultOpenSettings`; `config_editor.go` is removed and its `saveSingle` field-mapping logic moves into the settings page's commit sink.

### Interaction

- ↑↓ move across focusable fields (section titles are skipped).
- Enter activates the focused field per its widget kind (inline text edit, masked edit, open select picker).
- Within a select picker: ↑↓ choose, enter commit, esc cancel.
- Esc/q cancels an in-progress edit if editing; otherwise closes the page.
- Footer shows nav hints and the last save/error status.

## Testing

- **`internal/form` package** — per-widget unit tests: `TextField` (edit commit/cancel), `MaskedField` (blank-on-edit, display masking), `SelectField` (open picker, choose, commit, cancel), `ToggleField` (flip), `ReadOnlyField` (enter no-op). `Form` nav/focus (skips section headers, clamps at ends), section-render golden.
- **Settings page** — driver test against a fake agent client: edit a text field → `UpdateConfig` called with the right field; pick a select → RPC called; change permission-mode → RPC called + `settingsPermissionModeChangedMsg` emitted; change accent-color → `settingsColorChangedMsg` emitted; render golden of the full page.
- **Slash registry** — `/s`, `/settings`, `/config` all resolve to `ResultOpenSettings`; prefix-match and alias tests.
- **Root model** — handling `settingsPermissionModeChangedMsg` flips the chip; `settingsColorChangedMsg` updates `promptBorderColor`.

## Status

Implemented. Built via subagent-driven development on branch feat/settings-page. New `internal/form` widget package (Field interface + ReadOnly/Text/Masked/Select/Toggle widgets + Form) and `settingsPage` content page; `/s` `/settings` `/config` route to it; old `config_editor.go` removed.

## Open Questions / Notes

Resolved during brainstorming:
- Replace `/config` (not a parallel page). — replace.
- Content: fold in scattered settings + dedicated UI/Theme section. — yes.
- Edit UX: pickers for enums, text for free-form, plus whatever widgets are needed; build more as required. — extensible `Field` widget layer.
- Command binding: `/s` + `/settings` + `/config` alias. — yes.
- Widget layer: new `internal/form` package (not extending `RowList`). — new package.
- Accent color: `SelectField` over palette names + free-text hex escape hatch (no dedicated `ColorField` in V1). — confirmed.

Still open:
- Future `ui.yaml` UI prefs beyond accent color (out of V1 scope; the UI/Theme section leaves room).
- Whether section navigation should later gain `tab`/`shift+tab` section-jumping in addition to ↑↓ (deferred; ↑↓ is sufficient for V1).
