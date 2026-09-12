# Conversation search verification

## Automated checks performed

From `source/clients/cli`:

- `go test ./internal/ui -run TestConversationSearch -count=1`
- `go test ./internal/ui ./internal/slash -count=1`
- `go build ./...`

All passed on the final implementation. `git diff --check` also passed during diff review.

Tests cover scope filtering; readable Markdown and code; literal Unicode matching and terminal-cell positions (wide characters, combining marks, and joined emoji); active/total count and wrapping navigation; opening beneath the title; closing with the draft intact; keyboard/paste isolation during pending approval; progressive-history indication and active-message retention; streamed text and activity-glyph exclusion; conversation replacement; narrow-field layout; off-screen jumps; selection/copy; scrollbar dragging and wheel scrolling while approval is pending; modal precedence; and table-cell matching across resizing.

Two targeted regressions were observed before their fixes: a narrow search row hid the history-loading state behind the match count, and optional wrap boundaries invented concatenated-word matches such as `gammadelta` for `gamma delta`. The first now prioritizes a compact loading indicator, and the second recovers whitespace from readable message text rather than treating all line breaks as optional.

## Manual checks not performed

No interactive terminal session, native clipboard inspection, or terminal screenshot check was performed. Automated tests invoke the UI event/layout/rendering paths but do not prove a particular terminal delivers Ctrl+F or Shift+Enter. No performance benchmark was run; caching and row-indexed highlights were verified by code inspection, not benchmark claims.

## Scope and implementation notes

The user clarified readable message text rather than raw Markdown syntax. Search uses rendered message projections for match/highlight positions, with parsed readable message text used only to recover wrap whitespace. It does not search or fetch tool bodies, add a server endpoint, or use a model. Matches identify the message plus occurrence; history prepends retain that identity. Complex table rearrangements can change occurrence order within a message, so this is not a source-offset navigation API.

Folded superseded assistant replies are excluded until expanded; search does not automatically reveal censored/superseded content. Tool output and application status entries remain excluded regardless of expansion.

The feature lives in the isolated `feat/conversation-search` worktree. No merge, push, runtime restart, or installed-binary replacement was performed.
