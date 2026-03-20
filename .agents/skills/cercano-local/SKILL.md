---
name: cercano-local
description: Run prompts against local AI models via Cercano and Ollama. Use this for local inference — faster, private, and zero cost. Handles chat-style queries and agentic code generation with automatic validation. Offload summarization, explanation, code writing, and general LLM tasks to a local model instead of sending them to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Local Inference

Run prompts against local AI models through Cercano's MCP interface. Cercano routes requests to Ollama for local inference.

## MCP Tool

**Tool name:** `cercano_local`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `prompt` | string | Yes | The prompt to run against local models. |
| `file_path` | string | No | Target file path for code changes. When provided with `work_dir`, enables the agentic code generation loop with validation. |
| `work_dir` | string | No | Working directory for code validation (go build/test). When provided with `file_path`, enables the agentic code generation loop. |
| `context` | string | No | Additional context such as existing code or file contents. |
| `conversation_id` | string | No | Conversation ID for multi-turn support across calls. |

## Modes

### Chat Mode
Provide only `prompt` (and optionally `context`) for a direct LLM call. The response is the model's text output.

### Agentic Code Generation Mode
Provide `prompt`, `file_path`, and `work_dir` to enable a generate-validate loop. Cercano will:
1. Generate code based on the prompt
2. Write it to the target file
3. Run validation (build/test) in the working directory
4. If validation fails, self-correct and retry

## Examples

**Chat query:**
```json
{
  "prompt": "What are the SOLID principles in software design?"
}
```

**Code generation with context:**
```json
{
  "prompt": "Add error handling to this function",
  "file_path": "internal/handler/auth.go",
  "work_dir": "/project",
  "context": "func Login(w http.ResponseWriter, r *http.Request) { ... }"
}
```

**Multi-turn conversation:**
```json
{
  "prompt": "Now refactor that to use the repository pattern",
  "conversation_id": "conv-abc123"
}
```
