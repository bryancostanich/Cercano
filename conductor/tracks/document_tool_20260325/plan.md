# Track Plan: cercano_document — Local Code Documentation Tool

## Phase 1: Go AST Parser & Symbol Extraction

### Objective
Build the core Go file parser that identifies undocumented exported symbols and their source ranges.

### Tasks
- [x] Task: Create `internal/document/` package with Go AST parsing logic.
    - [x] Implement `ParseGoFile(filePath string) ([]Symbol, error)` that returns all exported symbols.
    - [x] Symbol struct: Name, Kind (func/method/type/interface/const), StartLine, EndLine, Body (source text), HasDoc (bool).
    - [x] Use `go/parser` with `parser.ParseComments` to detect existing doc comments.
    - [x] Filter to only exported (uppercase) symbols without existing doc comments.
    - [x] Red/Green TDD: TestParseGoFile_ExportedFunctions, TestParseGoFile_SkipsDocumented, TestParseGoFile_Methods, TestParseGoFile_Types, TestParseGoFile_Interfaces.
- [x] Task: Add symbol body extraction.
    - [x] Extract the full source text of each symbol (signature + body) for sending to the model.
    - [x] For types/interfaces, include field/method list.
    - [x] Red/Green TDD: TestSymbolBody_Function, TestSymbolBody_Interface.

## Phase 2: Doc Comment Generation via Local Inference

### Objective
Generate doc comments for each symbol using the local model and format them as valid Go doc comments.

### Tasks
- [x] Task: Implement prompt building and doc comment formatting.
    - [x] Build focused prompt per symbol (see spec Section 7).
    - [x] Parse response: strip any accidental `//` prefixes the model might add, trim whitespace.
    - [x] Format as Go doc comment: prepend `// ` to each line, ensure first line starts with symbol name.
    - [x] Support "minimal" (1-2 lines) and "detailed" (multi-line with params) styles via prompt variation.
    - [x] Red/Green TDD: TestBuildPrompt_Minimal, TestBuildPrompt_Detailed, TestFormatAsGoDoc, TestFormatAsGoDoc_StripsPrefixes.
- [x] Task: Handle model failures gracefully (in MCP handler).
    - [x] If model returns empty or unparseable response, skip the symbol and include in summary.

## Phase 3: File Writing & Safety

### Objective
Insert generated doc comments into the source file safely with backup and validation.

### Tasks
- [x] Task: Implement backup mechanism.
    - [x] `BackupFile(filePath string) (backupPath string, error)` — copy to `.cercano/backups/<filename>.<unix_timestamp>`.
    - [x] `RestoreFile(filePath, backupPath string) error` — copy backup back to original path.
    - [x] Red/Green TDD: TestBackupFile_CreatesBackup, TestRestoreFile_RestoresOriginal.
- [x] Task: Implement doc comment insertion.
    - [x] `InsertDocComments(filePath string, edits []DocEdit) error` where DocEdit is {Line int, Comment string}.
    - [x] Work backwards from end of file to preserve line numbers during insertion.
    - [x] Run `go/format.Source()` on the result to ensure valid formatting.
    - [x] Red/Green TDD: TestInsertDocComments_SingleFunction, TestApplyEdits_MultipleSymbols, TestApplyEdits_PreservesExisting.
- [x] Task: Implement post-write validation.
    - [x] Use `go/format.Source()` as validation (integrated into InsertDocComments).
    - [x] If validation fails, handler restores from backup.
    - [x] Red/Green TDD: TestApplyEdits_OutOfRange, TestInsertDocComments_FormatsResult.

## Phase 4: MCP Tool Handler & Integration

### Objective
Wire everything together as a `cercano_document` MCP tool.

### Tasks
- [x] Task: Add `DocumentRequest` struct and register `cercano_document` tool in `internal/mcp/server.go`.
    - [x] Parameters: file_path (required), style (optional), project_dir (optional), dry_run (optional).
    - [x] Register in `registerTools()` with handler `handleDocument`.
    - [x] Red/Green TDD: TestHandleDocument_MissingFilePath, TestHandleDocument_FileNotFound.
- [x] Task: Implement `handleDocument` handler.
    - [x] Orchestrate: parse -> backup -> generate per symbol -> insert -> validate -> summarize.
    - [x] In dry_run mode: parse and report symbols that would be documented, skip generation/write.
    - [x] Build summary response: list documented symbols, skipped symbols, any errors.
    - [x] Emit telemetry via `s.emitEvent()`.
    - [x] Red/Green TDD: TestHandleDocument_DryRun, TestHandleDocument_EndToEnd, TestHandleDocument_AllDocumented.
- [x] Task: Add `cercano_document` to `builtinSkills()` in `internal/server/skills.go`.
- [x] Task: Create `.agents/skills/cercano-document/SKILL.md`.
- [x] Task: Conductor - User Manual Verification 'MCP Tool Handler & Integration' (Protocol in workflow.md)
    - Verified dry_run mode reports correct symbols
    - Fixed grouped const/var/type bug (all shared same StartLine) — now uses spec position for grouped decls
    - Live tested on parser.go (6 grouped constants) — comments inserted at correct lines
    - Live tested on ollama.go (8 methods on real engine file) — all comments accurate, file compiles, tests pass

## Phase 5: Skill Definition & Documentation

### Objective
Register the skill and update project documentation.

### Tasks
- [x] Task: Update README with `cercano_document` tool description.
- [ ] Task: Conductor - User Manual Verification 'Skill Definition & Documentation' (Protocol in workflow.md)
