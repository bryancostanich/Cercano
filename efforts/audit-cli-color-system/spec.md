# Audit: CLI color system uses named theme items

## Problem

The daylight theme exposed several readability bugs in succession: sent prompts,
fenced code blocks, live activity text, compacting status text, and Bash/tool
headers all had colors or terminal attributes that were not fully controlled by
semantic theme items. Each individual fix improved one surface, but the common
failure mode remains: render code can still introduce raw color literals,
hardcoded red/green/blue interpolation endpoints, ANSI color sequences, or
terminal faint styling that bypasses the theme contract.

Cercano's CLI should make color intent explicit. Render paths should say "tool
args", "activity base", "selection background", or "code block canvas" rather
than spelling `#RRGGBB`, `38;2;...`, `lipgloss.Color(...)`, or naked
`Faint(true)` in UI code.

## Chosen theme boundary

Use **named tokens plus centralized derived colors**.

- UI/render packages should not contain bare color literals or raw ANSI color
  sequences for product UI.
- `internal/theme` owns palette values, semantic style names, and derived color
  helpers such as darkening, contrast foreground selection, or animation ramps.
- Tests may assert concrete ANSI/RGB values, but those assertions should be
  either in theme tests or clearly tied to a named theme token.
- Generated/probe/demo code is out of scope unless it ships in normal builds.

This avoids two bad extremes: it is stricter than spot-fixing visible bugs, but
it does not force every computed shade in an animation or overlay to become a
static palette field.

## Current audit findings to resolve

Initial read-only audit found these likely remediation areas:

1. `source/clients/cli/internal/ui/selection.go`
   - `selectionBg` is a raw `\x1b[48;2;88;130;158m` sequence in UI code.
   - It should become a theme-owned selection background style or helper.

2. `source/clients/cli/internal/ui/model.go`
   - `animateSpinnerGlyphAt` uses hardcoded amber RGB endpoints.
   - `progressColorAtForPalette` still has hardcoded lime/white defaults for
     the legacy cracker path.
   - `fadeColor`, `rgbColor`, `colorRGB`, and `isLightColor` are UI-local color
     utilities; they belong in the theme package if they remain generally used.
   - The compacting meter chooses contrast foregrounds in UI code; the semantic
     rule belongs in theme.

3. `source/clients/cli/internal/theme/styles.go`
   - `BufferCodeBlock` currently uses `lipgloss.Color(hexBgDeep)` directly
     instead of a palette/style token.
   - `BypassFlag` hardcodes a dark foreground literal.
   - These are in the theme package, so they are not render-layer leaks, but
     they should still become named palette/style fields so the theme contract
     is complete.

4. Tool, activity, sent prompt, and code-block fixes recently added regression
   tests with literal ANSI fragments. Those are acceptable as tests, but the
   implementation side should be covered by a static audit so future literals do
   not reappear in render code.

## Desired behavior

- All visible CLI UI color decisions are expressed through named theme palette
  items, named styles, or theme-owned derived helpers.
- Daylight and dark themes remain readable for:
  - sent prompts,
  - assistant prose and markdown,
  - fenced code blocks,
  - live activity/thinking indicators,
  - context meter and compacting overlay,
  - tool/Bash headers and status text,
  - selection highlight,
  - bypass/permission/status flags.
- The codebase has a lightweight guard that flags new render-layer hardcoded
  colors before they ship.

## Scope

In scope:

- `source/clients/cli/internal/theme`
- `source/clients/cli/internal/ui`
- `source/clients/cli/internal/render` only where markdown/theme style config is
  involved
- CLI unit/golden tests needed for this cleanup

Out of scope:

- Server/agent color handling, unless the CLI imports it directly.
- Redesigning the visual identity of all themes.
- Replacing Glamour/Chroma syntax highlighting. Code block readability is
  covered by the existing dark canvas; deeper syntax theming can be a later
  feature.
- Packaging, docs, or Homebrew work.

## Acceptance

1. No product render code under `source/clients/cli/internal/ui` or
   `source/clients/cli/internal/render` contains raw hex colors, raw `38;2` /
   `48;2` SGR sequences, or product-use `lipgloss.Color("...")` calls.
2. Product render code does not rely on naked `Faint(true)` for essential text
   contrast; any remaining faint usage is justified or theme-owned.
3. Theme-owned literals are named semantic palette/style items or centralized
   derived helpers, not anonymous one-off values inside render paths.
4. A focused static audit test fails if new hardcoded render-layer color
   literals are introduced outside an approved allowlist.
5. Existing daylight fixes remain covered by regression tests.
6. `cd source/clients/cli && go test ./... -count=1` passes.
