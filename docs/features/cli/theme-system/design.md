# Theme System

## Overview / Goal

Give the cercano-cli a real theme system: several named built-in themes, the ability to switch between them live, and a way to create, edit, save, delete, and import **custom** themes — all from within the settings page. Today there is exactly one theme (`cracker`), hardcoded at startup, with no persistence and no switching.

A theme controls the **entire** look — chrome (backgrounds, borders, primary/accent/info, success/warn/error) *and* content (scrollback links, code spans, the user-prompt echo fill, assistant markdown prose). Switching or editing repaints the UI **live**.

**What changes:**
- New `Theme` type holding the full color set; today's `Palette` chrome colors plus the scrollback/markdown colors that are currently package-global (`BufferLink`, `BufferCode`, `BufferLime`, `BufferError`, `BufferUserBg`) fold into it.
- `NewStyles` and a new palette-parameterized `MarkdownStyle` derive from a `Theme` (replacing the hardcoded `CrackerMarkdownStyle` and the global `Buffer*` vars).
- A theme registry (built-ins in code + custom themes loaded from disk) and a live `Model.applyTheme`.
- New CLI config file `~/.config/cercano/ui.yaml` storing the active theme name; custom themes as `~/.config/cercano/themes/<name>.yaml`.
- Two new `internal/form` widgets: `ColorField` (swatch + hex) and `ButtonField` (action row).
- Settings-page Theme sections (selector + grouped color rows + action rows).

**What does NOT change:**
- The settings page architecture, the `Field`/`Form` kit (extended, not reworked), the `contentPage` model.
- The default look: `cracker` stays the default and is byte-for-byte the same colors.
- `/color` (per-session prompt-border override) stays as a quick shortcut.
- The agent/server side — themes are purely a CLI concern.

## Design / Approach

### Design principles (load-bearing)
1. **Algorithmic over LLM** — no model anywhere; color parsing, validation, swatch rendering, registry lookup are deterministic.
2. **One source of truth per look** — a `Theme` fully determines every color; `Styles` and the markdown `StyleConfig` are pure functions of it. No color is read from a global or a literal at render time.
3. **Live, total propagation** — applying a theme rebuilds styles everywhere and flushes any cached renderers, so no stale color survives a switch.
4. **Extend the widget kit, don't fork it** — color and button rows are new `Field` implementations, consistent with text/select/toggle.

### The `Theme` type (`internal/theme/theme.go`)

```go
// Theme is a named, complete color set. Every cercano-cli color derives from a
// Theme; there are no other color sources at render time.
type Theme struct {
	Name string

	// Chrome (top bar, footer, panels, borders, meters).
	BgDeep    color.Color
	Surface   color.Color
	BorderDim color.Color
	Border    color.Color
	Primary   color.Color
	Bright    color.Color
	DimAmber  color.Color
	Accent    color.Color
	Info      color.Color
	Muted     color.Color
	Success   color.Color
	Warn      color.Color
	Error     color.Color

	// Content (scrollback + markdown prose).
	BufferLink   color.Color // markdown links
	BufferCode   color.Color // inline code / code-fence lang
	BufferLime   color.Color // tool ✓, focus caret, echoed user ▶
	BufferError  color.Color // tool ⚠
	BufferUserBg color.Color // fill behind echoed user prompts
}
```

`Palette` is retained as the chrome subset that `NewStyles` consumes (a `Theme.Palette()` accessor returns it), so the existing `Styles` builder changes minimally. `NewStyles(t)` and `MarkdownStyle(t)` both take a `Theme`. The package-global `Buffer*` `lipgloss.Color` vars and `CrackerMarkdownStyle()` are removed; their consumers read from `Styles`/the active theme instead.

Color (de)serialization: a theme persists as YAML `name` + a `colors:` map of `key: "#RRGGBB"`. A `parseHex`/`hexOf` pair converts between `color.Color` and `#RRGGBB`. Unknown/missing keys on load fall back to the corresponding `cracker` value (forward-compatible).

### Built-in themes (`internal/theme/builtins.go`)

Defined in code, always present, read-only:
- **cracker** *(default)* — amber + lime + cyan on charcoal (exact current values; `Cracker()` returns `themeCracker.Palette()`).
- **phosphor** — green CRT: greens + dim-green on near-black.
- **synthwave** — magenta/cyan/violet on deep indigo.
- **daylight** — light theme: dark text + warm accents on warm paper.

`BuiltinThemes() []Theme` returns them in display order; `cracker` first.

### Registry + live apply

`internal/theme` exposes a `Registry` (ordered list, lookup by name, `IsBuiltin(name)`), seeded with built-ins and extended with custom themes loaded from disk (next section).

`Model.applyTheme(t Theme)` is the heart of live switching:
1. `m.theme = t; m.palette = t.Palette(); m.styles = theme.NewStyles(t); m.markdownStyle = theme.MarkdownStyle(t)`.
2. `m.chat.SetStyles(m.styles, m.palette, m.markdownStyle)` — chatView **flushes its per-width glamour renderer cache** and rebuilds, so prose/code recolor.
3. Re-propagate to any other persistent component that caches styles; transient pages (created on open) already read current styles.
4. `m.refreshViewport()` / relayout for a full repaint.

Because the user switches/edits themes *from inside the settings page*, the settings page also rebuilds itself after an apply (new styles for its chrome, new swatch colors, new editable/read-only state) — see the UI section.

`chatView.SetStyles` is the one genuinely tricky propagation: it must invalidate the cached glamour renderers (keyed by width) and re-render committed entries. This is called out as the primary risk/complexity.

### Persistence

- **Active theme** → `~/.config/cercano/ui.yaml` (new), `{ theme: "<name>" }`. This finally creates the planned CLI UI config; a `uiconfig` loader/saver lives in the CLI module (e.g. `pkg/uiconfig` or `internal/uiconfig`). Resolution: `$CERCANO_UI_CONFIG` → `~/.config/cercano/ui.yaml`. Missing file → default `cracker`.
- **Custom themes** → `~/.config/cercano/themes/<name>.yaml`, one file per theme. At startup the registry loads built-ins, then every `*.yaml` in the themes dir (custom names shadow nothing — a custom theme may not reuse a built-in name; collision is rejected on save). Invalid theme files are skipped with a logged warning, never fatal.

### New form widgets

- **`ColorField`** (`internal/form/color_field.go`) — renders `███ #RRGGBB` where the swatch is rendered in the field's own color (live). Editable: enter opens inline hex edit (reuse the `TextField` edit mechanics); commit validates `#RRGGBB` (reject + status message on bad input) and carries the new hex value. Read-only mode (built-in themes): swatch + hex shown, enter does nothing. Implements `Field`.
- **`ButtonField`** (`internal/form/button_field.go`) — a selectable action row rendered like `[ Save ]`; enter "commits" a sentinel so the form's `OnCommit` routes it to an action. Implements `Field`; `Editing()` always false. Disabled state (e.g. Save/Delete on a built-in) renders dim and is inert.

Both are additive — no change to existing widgets or the `Field` interface.

### Settings-page Theme sections

The settings page's single **UI / Theme** section expands into a small group of sections (still inside the one settings page — no separate page; a future "sub-page" navigation metaphor is explicitly deferred):

| Section | Rows |
|---|---|
| Theme | `theme` — SelectField over registry names (built-ins + custom). Select → `applyTheme` live + persist active name. |
| Theme · Chrome | one `ColorField` per chrome color (bg, surface, borders, primary, bright, dim, accent, info, muted, success, warn, error) |
| Theme · Content | one `ColorField` per content color (link, code, lime, error, user-bg) |
| Theme · Actions | `Save` (button), `Save As` (text → name), `Delete` (button), `Import` (text → path) |

**Editing model (working copy):** the settings page holds a *working theme* = an editable copy of the active theme. ColorField commits mutate the working theme in memory and call `applyTheme(working)` → live repaint. Built-in active theme → ColorFields are read-only and Save/Delete are disabled; to customize, **Save As `<name>`** clones the working colors to a new custom theme, switches to it, and makes the colors editable. For a custom active theme: **Save** writes the working theme to its yaml; **Delete** removes the file (and falls back to `cracker`); switching themes discards unsaved working edits (a `(unsaved)` marker on the Theme row signals dirty state).

After any apply/save/select the page rebuilds its form so swatches, values, editable/read-only state, and the dirty marker reflect reality.

**Commit routing:** the settings page's `onCommit` (today: config / permission / color) gains theme cases keyed by the new rows — `theme-select`, `color:<key>`, `theme-save`, `theme-save-as`, `theme-delete`, `theme-import`. The accent-color row is removed; the prompt border now derives from `theme.Accent` (with `/color` still able to override per session).

### `/theme` shortcut

`/theme` opens the settings page (same as `/s`). Optional nicety: open scrolled to the Theme section. Listed in `/help`.

## Testing

- **`theme` package** — round-trip a theme through YAML (`hexOf`/`parseHex`, missing-key fallback to cracker); `NewStyles`/`MarkdownStyle` pull from theme fields (no leftover global/literal); `cracker` built-in equals today's exact hexes (golden); registry ordering + `IsBuiltin` + name-collision rejection.
- **`form` package** — `ColorField` (edit→validate good hex commits; bad hex rejected with status, value unchanged; read-only inert; swatch present in View); `ButtonField` (enter commits sentinel; disabled inert).
- **`uiconfig`** — load missing → default cracker; load/save round-trip; env override path.
- **settings page** — selecting a theme calls `applyTheme` + persists; editing a ColorField on a custom theme mutates working + applies; on a built-in it's read-only and Save/Delete disabled; Save As clones+switches; Import(path) loads a file; render golden of the Theme sections.
- **live apply** — `applyTheme` rebuilds styles and `chatView.SetStyles` flushes the renderer cache (assert a cached renderer is rebuilt / cache key cleared); a committed markdown entry re-renders in new colors.

## Status

Implemented.

## Open Questions / Notes

Resolved during brainstorming:
- Apply timing → **live repaint**; switching a named theme applies+persists instantly, color edits preview live and persist on **Save**.
- Theme scope → **full fidelity** (chrome + content), grouped in the editor.
- Import/export → **files only** (`/`-style import by path; custom themes already are yaml files on disk).
- Built-in set → cracker (default), phosphor, synthwave, daylight (starter; tunable).
- Editor location → **inline sections in the settings page**, not a separate page; sub-page navigation deferred.

Still open / deferred:
- A real sub-page navigation metaphor for the settings page (deferred; revisit when more nested editors appear).
- Whether `/theme` should scroll-focus the Theme section (nice-to-have).
- Light themes (`daylight`) may expose contrast issues in styles tuned for dark backgrounds — validate during build; adjust specific styles if needed.

## Phasing (one spec, built in order)

1. **Infra** — `Theme` type + `Palette()` accessor; fold buffer/markdown colors in; `NewStyles(t)`/`MarkdownStyle(t)`; built-ins; registry; `Model.applyTheme` + `chatView.SetStyles` live propagation (default stays cracker, no behavior change yet).
2. **Persistence** — `uiconfig` (`ui.yaml`, active theme) + themes-dir loader + theme YAML (de)serialization + save/delete.
3. **UI** — `ColorField` + `ButtonField`; settings Theme sections (selector + grouped colors + actions); working-copy editing model; `/theme` shortcut; remove the accent-color row.
