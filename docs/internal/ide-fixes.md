# VS Code IDE Integration Fixes

**Status:** Shipped maintenance.

## Overview

The prior IDE-enhancements track was marked complete, but manual testing showed the code review/apply workflow was non-functional. This track fixed the specific `extension.ts` bugs blocking Apply Changes, Preview Changes, and Reject.

## Bugs fixed (all in `extension.ts`, commit 8dc94cb)

### Bug 1 — filePaths not passed to command handlers (Critical)
`followupProvider` stored `filePaths` in `result.metadata`, but the command `arguments` array only included `{ responseId }`. Both `cercano.applyChanges` and `cercano.previewChanges` received `args.filePaths === undefined` and silently no-op'd.
**Fix:** switched to `response.button()`, which correctly accepts VS Code commands with arguments; `followupProvider` removed for Apply/Preview/Reject.

### Bug 2 — Chat participant double-registered
`participant.iconPath` assignment and `context.subscriptions.push(participant)` appeared twice in `activate()`, causing duplicate response handling.
**Fix:** removed the duplicate registration.

### Bug 3 — Hardcoded WorkspaceEdit replace range
A hardcoded 100,000-line range (`new vscode.Range(new vscode.Position(0,0), new vscode.Position(100000,0))`) was used for file replacement, leaving trailing content and being semantically incorrect.
**Fix:** replace using the actual document line count.

### Bug 4 — followup command/prompt ambiguity
Followup items set both `command` and `prompt`, while the chat participant had an early-exit guard filtering those prompt strings — conflicting pathways, unreliable across VS Code versions.
**Fix:** `ChatFollowup.command` is a participant command, not a VS Code command; switched to `response.button()` (single, unambiguous command-based mechanism).

## Verification

- Client tests (`test/client.test.ts`) reviewed/updated; added tests for the followup command argument threading (Bug 1).
- Manual end-to-end in the Extension Development Host: file tree appears on a coding request; Apply writes file content correctly; Preview opens a diff view; Reject dismisses without writing; no duplicate responses.
- **Known limitation:** buttons persist after use (VS Code Chat API limitation; accepted).

## Out of scope

- gRPC backend / proto changes.
- New IDE features beyond the original enhancements track.
- Zed extension changes.
