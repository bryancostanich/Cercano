---
name: cercano-document
description: Use when the user wants to generate or update doc comments for Go code. Handles the entire read-think-write cycle locally — the host never sees the file contents. Supports dry_run mode to preview.
compatibility: Requires Cercano server running and connected to an Ollama instance. Currently supports Go source files only.
---

# Cercano Document

Generate doc comments for exported Go symbols using local AI and write them directly to the source file.

## MCP Tool

**Tool name:** `cercano_document`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| file_path | string | Yes | Path to the Go source file to document. |
| style | string | No | Doc comment style: "minimal" (1-2 sentences, default) or "detailed" (multi-line with params). |
| dry_run | bool | No | If true, report what would be documented without writing changes. |
| project_dir | string | No | Project root directory for context-aware responses. |

## How It Works

1. Parses the Go file using the standard go/ast package
2. Identifies exported symbols (functions, methods, types, interfaces, constants) without doc comments
3. Generates a doc comment for each using local inference (one symbol at a time)
4. Inserts comments at the correct positions and formats with gofmt
5. Returns a summary of what was documented

The host agent never sees the file contents — only the summary.

## Safety

- Creates a backup in `.cercano/backups/` before writing
- Validates the result with `go/format`
- Restores from backup if validation fails
- Skips symbols where the model returns garbage

## Examples

**Document a file:**
```json
{"file_path": "internal/engine/ollama.go"}
```

**Preview without writing:**
```json
{"file_path": "internal/engine/ollama.go", "dry_run": true}
```

**Detailed style:**
```json
{"file_path": "internal/engine/ollama.go", "style": "detailed"}
```
