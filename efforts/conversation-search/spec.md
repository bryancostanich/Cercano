# Current-conversation search

## Problem and motivation

Users need to find earlier text in the current conversation without relying on the terminal emulator's Find command. macOS Terminal can intercept Command-F before Cercano receives it, and terminal search cannot reliably navigate the application's full transcript.

## Goals

Provide deterministic, local, case-insensitive literal search over readable user and assistant message text in the current conversation, including their code blocks. Search readable message text across the full loaded transcript rather than only the visible viewport. Markdown formatting syntax is not searchable; rendered prose and code are. The user explicitly approved this clarification after reviewing the source-to-rendering distinction. Exclude tool calls and tool output, as well as application status notices and other non-message entries.

Both /search and Ctrl+F open and focus a search field immediately below the conversation title bar. /search <text> prepopulates the query. Opening search preserves the composer draft. The field displays the current match position and total match count, with explicit empty-query and no-match states.

Update results as the query changes. Highlight matches, distinguish the active match, and navigate to its location in the transcript. Enter selects the next match and Shift+Enter selects the previous match, wrapping at the ends. Escape closes search, removes search highlighting, and restores the underlying input focus without changing the composer draft. Leave the transcript at the viewed match so the user can continue reading.

During progressive loading of a resumed conversation, keep available messages searchable and show “Loading history…” alongside the current results. Update results as older messages arrive and remove the indicator when history loading completes. Do not describe partial results as a complete search. Reflect new and streaming message text in results without unnecessarily displacing the match the user is viewing. Do not persist matches or navigation state across conversation changes.

Mouse selection, copying, scrolling, and existing mouse navigation remain usable while search is open. Search must also work while an approval prompt is pending: the focused search field receives its own text and navigation keys, including y/n/c/d, without resolving approval. Closing search restores the still-pending approval interaction. Search does not bypass permissions or send its query as a conversation message.

## Non-goals

Do not search other conversations, tool arguments, or tool results. Do not add semantic search, regular expressions, model calls, a server-side search endpoint, or a persistent search index. Do not override macOS Terminal's Command-F binding. Do not fetch tool bodies for search or expand folded tool output.

## Constraints

Keep this functionality in the CLI and reuse its retained message text and existing progressive history-loading mechanism. Preserve the agent/server boundary and existing transport interfaces. Account for the extra search row in viewport sizing, scrolling, hit testing, and mouse selection coordinates, including after resizing and closing the field. Highlighting must not corrupt terminal styling, Unicode text, wrapped lines, code blocks, or copied message text.

Ctrl+F becomes search in the conversation view; Page Down remains available for paging. Avoid changing unrelated content-page key bindings. Search must not steal keys from higher-priority unrelated dialogs. Do not block typing or ordinary conversation rendering with unnecessary history-loading gates.

## Decisions

The user approved current-conversation scope, /search and Ctrl+F entry points, and placement immediately below the title bar. The user explicitly excluded tool output and approved a loading indicator while older history arrives.

Local matching is the selected approach. The CLI already retains loaded user and assistant message text, so a server-side search endpoint would add an unnecessary interface for this scope. Lazy tool-body loading does not constrain this design because tool content is excluded. Progressive message loading is handled by updating local matches and showing the loading indicator, not by introducing another loading path.

There is no remaining solution-shape fork requiring a comparison table. These decisions supersede the earlier proposal to search tool output or perform matching in the server.

## Acceptance criteria

A user can open search through either approved entry point, locate text outside the viewport, navigate forward and backward, and close search without losing their draft. User and assistant code blocks are searchable; matching text present only in tool content is not returned. Match highlights and jump targets remain correct after wrapping, resize, and older-history insertion. The loading indicator accurately follows progressive history loading. Search and mouse selection remain usable during pending approval without accidentally approving or denying a call. Switching conversations cannot navigate to stale matches from the prior conversation.

Verification includes focused matching and navigation tests, UI tests for entry points, layout and focus, progressive-history and streaming updates, tool exclusion, Unicode and wrapping, and approval/mouse interaction regressions. Run the CLI UI package tests and build the CLI; do not require unrelated server or full end-to-end suites for this CLI-local change. Record any manual terminal checks separately from automated results.
