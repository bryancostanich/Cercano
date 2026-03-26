# Track Specification: cercano_document — Local Code Documentation Tool

## 1. Job Title
Add a `cercano_document` MCP tool that generates doc comments for source code files using local inference, writing them directly to disk so the host agent never sees the file contents.

## 2. Overview
Cloud agents spend significant tokens reading files, reasoning about doc comments, and writing them back. This is mechanical work that a local model can handle well — the context is narrow (one function at a time), the output format is predictable (doc comments), and the source code is the ground truth.

`cercano_document` reads a source file, identifies undocumented exported symbols, generates doc comments via the local model, and writes the edits directly to disk. The host receives only a short summary of what was documented, saving potentially thousands of cloud tokens per file.

### Design Principles
- **Host never sees file contents** — the entire read-think-write cycle happens locally
- **Function-at-a-time for Go** — use `go/ast` to parse, document one symbol at a time, insert surgically
- **Full-file rewrite fallback for other languages** — with backup and validation
- **Non-destructive** — skip symbols that already have doc comments
- **Safe** — backup before writing, validate after (syntax check), restore on failure

## 3. User Experience

**Host invocation:**
```
cercano_document(file_path: "internal/engine/ollama.go")
```

**Response to host (all the host sees):**
```
Documented 4 of 6 exported symbols in internal/engine/ollama.go:
  + OllamaEngine (struct)
  + NewOllamaEngine (function)
  + Complete (method)
  + ListModels (method)
  Skipped: Name (already documented), CompleteStream (already documented)
```

**What happens on disk:**
The file is updated in place with doc comments inserted above each undocumented exported symbol. A backup is stored at `.cercano/backups/<filename>.<timestamp>` before any writes.

## 4. Parameters

| Parameter    | Type   | Required | Description |
|-------------|--------|----------|-------------|
| `file_path` | string | yes      | Absolute or project-relative path to the source file |
| `style`     | string | no       | `"minimal"` (default) — one-line summaries; `"detailed"` — multi-line with param/return docs |
| `project_dir` | string | no    | Project root for context loading |
| `dry_run`   | bool   | no       | If true, report what would be documented but don't write. Default: false |

## 5. Supported Languages

### Phase 1: Go (AST-based)
- Parse with `go/ast` / `go/parser`
- Identify exported functions, methods, types, interfaces, constants
- Skip any symbol that already has a preceding doc comment
- Generate one doc comment per symbol via local model (small, focused prompt)
- Insert at the correct line position using byte offsets from AST
- Validate with `go vet` after all edits

### Phase 2: General languages (rewrite-based)
- Read entire file
- Prompt local model to add doc comments to undocumented exported/public symbols
- Write full file back
- Validate: file still parses (language-specific check where available)
- Backup/restore on failure

Phase 2 is out of scope for this track — noted here for future direction.

## 6. Safety

1. **Backup**: Before any write, copy original to `.cercano/backups/<filename>.<unix_timestamp>`
2. **Validation**: After writing, run `go vet` on the file. If it fails, restore from backup and report the error.
3. **Atomic per-symbol**: Each symbol is documented independently. If the model returns garbage for one symbol, skip it and continue with the rest.
4. **dry_run mode**: Let the host (or user) preview what would change before committing.

## 7. Prompt Strategy

For each undocumented symbol, send a focused prompt to the local model:

```
You are a Go documentation writer. Write a GoDoc comment for the following symbol.
Rules:
- Start with the symbol name
- Be concise — one or two sentences for minimal style
- Do not repeat the function signature
- Do not add code examples
- Return ONLY the comment text (without the // or /* */ markers)

Symbol:
func (e *OllamaEngine) Complete(ctx context.Context, model, prompt, systemPrompt string) (string, error) {
    ...body...
}
```

Cercano then wraps the response in `// ` prefix lines and inserts above the symbol.

## 8. Non-Goals
- Documenting unexported/private symbols
- Modifying existing doc comments
- Documenting non-code files (markdown, config)
- Phase 2 multi-language support (future track)
- Inline comments within function bodies
