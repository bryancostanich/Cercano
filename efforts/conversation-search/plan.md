# Current-conversation search

Implement the approved spec.md in the CLI only. Search retained user and assistant message text, exclude tool content, and display the search field immediately below the title bar. No server interface or model calls. The specification was approved in conversation before this plan was written.

## Phase 1 — Confirm integration points and implement local matching

Objective: map the existing entry, rendering, and progressive-loading interfaces narrowly, then build deterministic matching with stable message-relative locations. Files: source/clients/cli/internal/ui/chat_view.go and the existing entry definitions; new focused search implementation and test files in that UI package. Inspect scoped instructions before editing. Tests: role filtering, empty query, case-insensitive literals, repeated matches, code blocks, Unicode offsets, and next/previous wraparound.

- [x] Use delegated, bounded reconnaissance to identify entry identity, source-to-rendered-text mapping, history-loading state, command registration, input routing, and existing tests.
- [x] Establish an isolated worktree using the worktree capability and preserve unrelated working-tree changes.
- [x] Add local search state and matching over user and assistant message text only.
- [x] Represent matches with message-relative locations rather than viewport row numbers, allowing prepended history and resizing without stale navigation.
- [x] Add focused matching and navigation tests, including explicit exclusion of tool calls, tool results, and status notices.
- [x] If rendering or entry identity violates the approved assumptions, surface that specific finding before changing the design.

## Phase 2 — Search field, commands, and input focus

Objective: expose search directly beneath the title bar without disrupting the composer or approval controls. Files: source/clients/cli/internal/ui/model.go, focused search UI files, and the existing slash-command/help definitions identified in phase 1. Tests: both entry points, prepopulated query, draft preservation, input routing, pending approvals, close/reopen, narrow terminal layout, and unrelated modal precedence.

- [x] Add /search and /search <text> command handling and command discoverability.
- [x] Bind Ctrl+F to search in the conversation view, retaining Page Down and unrelated content-page bindings.
- [x] Render the search field under the title bar with query, active/total match count, empty-query and no-match states, and navigation hints where space allows.
- [x] Route text, paste, Enter, Shift+Enter, and Escape to the focused search field without submitting a message or altering the composer draft.
- [x] Keep pending approval intact while search captures y/n/c/d; restore the pending interaction on close and preserve higher-priority modal behavior.
- [x] Account for the extra row in viewport height and all mouse-coordinate offsets, both when opening and closing search and after resize.
- [x] Add focused UI tests for these entry, layout, and focus contracts.

## Phase 3 — Highlighting, transcript navigation, and live history

Objective: show and navigate matches across the full loaded message text while keeping results synchronized with conversation changes. Files: source/clients/cli/internal/ui/chat_view.go, focused search files, model lifecycle/history handlers, and adjacent rendering/selection tests. Tests: off-screen navigation, styled and wrapped text, code blocks, Unicode, copying, history prepend, streaming updates, reset, and resize.

- [x] Map source match locations to rendered transcript positions using the existing rendering pipeline without treating terminal styling bytes as searchable content.
- [x] Highlight matches and distinguish the active match while preserving existing styles and copied text.
- [x] Jump to next/previous matches across viewport boundaries and leave the transcript at the viewed location when closing search.
- [x] Recompute affected results when text streams or older history arrives, preserving the active match where possible instead of stealing the user's position.
- [x] Display Loading history… while progressive loading is active, keep partial results usable, and clear the indicator when loading completes.
- [x] Reset conversation-specific search state on conversation replacement or clearing so no stale match targets survive.
- [x] Verify mouse selection, release-to-copy, wheel scrolling, and scrollbar dragging with search open, including while approval is pending.

## Phase 4 — Verify and checkpoint

Objective: verify the complete CLI-local feature against the approved acceptance criteria and document the entry points. Files: search and existing UI regression tests, the relevant CLI user documentation, and this effort plan. Tests: focused search cases, the full CLI UI package, command-package tests if command registration changed, and the CLI build. Manual terminal checks are reported separately and only if actually performed.

- [x] Run focused matching, rendering, layout, history, confirmation, and mouse-interaction tests throughout implementation.
- [x] Run go test ./internal/ui -count=1 from source/clients/cli, plus the relevant command-package tests if applicable.
- [x] Build the CLI using its existing module/build conventions; do not require unrelated server or full end-to-end suites.
- [x] Where terminal access permits, manually check under-title placement, Ctrl+F, Shift+Enter, Unicode highlighting, copying, loading indication, and pending-approval focus; explicitly report any unperformed checks.
- [x] Document /search, Ctrl+F, navigation keys, excluded tool content, and the history-loading indicator in the relevant CLI documentation.
- [x] Review the scoped diff, update plan checkboxes with actual outcomes, and checkpoint explicit feature paths with a conventional commit. Do not push unless asked.
