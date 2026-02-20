# Track Specification: VS Code IDE Integration Fixes

## 1. Job Title
Fix the broken code review and apply workflow in the VS Code extension.

## 2. Overview
The previous IDE enhancements track (ide_enhancements_20260203) was marked complete, but manual testing revealed the code review/apply workflow is non-functional. This track identifies and fixes the specific bugs in `extension.ts` that prevent Apply Changes, Preview Changes, and Reject from working correctly.

## 3. Known Bugs

### Bug 1 — filePaths not passed to command handlers (Critical)
The `followupProvider` stores `filePaths` in `result.metadata` but the `arguments` array passed to VS Code commands only includes `{ responseId }`. Both `cercano.applyChanges` and `cercano.previewChanges` commands receive `args.filePaths === undefined`, causing the for-loop to silently exit with no action taken.

### Bug 2 — Chat participant double-registered
`participant.iconPath` assignment and `context.subscriptions.push(participant)` appear twice in `activate()` — once after participant creation and again at the end of the function. This causes the participant to be registered twice, leading to duplicate response handling.

### Bug 3 — WorkspaceEdit replace range is hardcoded
```typescript
new vscode.Range(new vscode.Position(0, 0), new vscode.Position(100000, 0))
```
A hardcoded 100,000-line range is used for file replacement. This can leave trailing content on files and is not semantically correct. Should replace using the actual document line count.

### Bug 4 — followup command/prompt ambiguity
Followup items have both `command` and `prompt` fields set. The chat participant also has an early-exit guard filtering those exact prompt strings. The two pathways conflict; neither is guaranteed to work correctly across VS Code versions.

## 4. Requirements

- All three followup actions (Apply, Preview, Reject) must work correctly end-to-end.
- File paths must be correctly threaded from the chat response through to command handlers.
- No duplicate participant registration.
- WorkspaceEdit range must use actual document length.
- The followup pathway must use a single, unambiguous mechanism (command-based).

## 5. Acceptance Criteria
- [ ] User asks Cercano to generate code; file tree appears in chat response.
- [ ] Clicking "Preview Changes" opens a VS Code diff view showing proposed vs. current file content.
- [ ] Clicking "Apply Changes" writes the generated content to disk correctly.
- [ ] Clicking "Reject" dismisses the changes without modifying any files.
- [ ] No duplicate responses or command invocations on any action.
- [ ] Existing unit tests pass with no regressions.

## 6. Out of Scope
- Changes to the gRPC backend or proto definitions.
- New IDE features beyond what was specified in ide_enhancements_20260203.
- Zed extension changes.
