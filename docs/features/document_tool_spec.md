# Local Code Documentation Tool (`cercano_document`)

## Overview
`cercano_document` is an MCP tool that generates doc comments for source code using local inference and writes the edits directly to disk, so the host cloud agent never sees the file contents. Documenting code is mechanical, narrow-context work (one symbol at a time, predictable output format) — a good fit for local offload. The host receives only a short summary of what was documented, saving potentially thousands of cloud tokens per file. Phase 1 (shipped) covers Go via AST parsing.

## Design / Architecture
The tool lives in `internal/document/` and is wired as an MCP handler (`handleDocument`) registered in `internal/mcp/server.go`.

- **Go AST parsing** — `ParseGoFile` uses `go/parser` with `parser.ParseComments` to enumerate exported symbols. Each `Symbol` carries Name, Kind (func/method/type/interface/const), StartLine, EndLine, Body (source text), and HasDoc. Output is filtered to exported symbols that lack an existing doc comment. Symbol body extraction includes the full signature + body, and for types/interfaces the field/method list, so the model has enough context.
- **Per-symbol generation** — For each undocumented symbol, a focused prompt is sent to the local model asking for a GoDoc comment (start with the symbol name, concise, no signature repetition, no code examples, comment text only). The response is sanitized (strip stray `//`, trim) and formatted as a Go doc comment with `// ` prefixes. Two styles: `minimal` (default, 1–2 lines) and `detailed` (multi-line with param/return docs), selected by prompt variation.
- **Surgical insertion** — `InsertDocComments` applies `DocEdit{Line, Comment}` edits working backwards from end of file to preserve line numbers, then runs `go/format.Source()` to guarantee valid formatting (this doubles as post-write validation).
- **Safety** — `BackupFile` copies the original to `.cercano/backups/<filename>.<unix_timestamp>` before any write; `RestoreFile` reverses it. If validation fails, the handler restores from backup and reports the error.

## Key Behaviors / Capabilities
- Documents only exported (uppercase) symbols; skips any symbol that already has a doc comment (non-destructive).
- Handler orchestration: parse → backup → generate per symbol → insert → validate → summarize.
- Atomic per-symbol — garbage output for one symbol is skipped, the rest continue.
- `dry_run` mode parses and reports which symbols would be documented without writing.
- Returns a host-facing summary listing documented symbols, skipped symbols, and any errors; emits telemetry via `emitEvent`.
- Registered as a builtin skill (`internal/server/skills.go`) with a `.agents/skills/cercano-document/SKILL.md`.

### Parameters
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `file_path` | string | yes | Absolute or project-relative path to the source file |
| `style` | string | no | `minimal` (default) or `detailed` |
| `project_dir` | string | no | Project root for context loading |
| `dry_run` | bool | no | Report without writing (default false) |

## Notable Decisions / Constraints
- Host never sees file contents — the full read-think-write cycle is local.
- Go-only AST approach (Phase 1). A general-language full-file-rewrite fallback (Phase 2) was specified as future direction but is out of scope.
- Grouped const/var/type declarations originally shared a single StartLine (bug); fixed during verification to use the spec position per grouped declaration. Verified live on `parser.go` (6 grouped constants) and `ollama.go` (8 methods).
- Non-goals: documenting unexported symbols, modifying existing doc comments, documenting non-code files, and inline comments within function bodies.
