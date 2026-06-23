# Local-First AI Assistant (MVP)

## Overview
This is the foundational MVP of Cercano: a local-first AI development assistant that intelligently leverages both local and cloud models. The core is a Go application with a semantic "smart router" that directs developer requests to the most appropriate model, exposed over gRPC, plus a decoupled IDE abstraction layer (initially a VS Code-compatible extension targeting Antigravity, with a scaffolded Zed extension). The MVP focused on common, mechanical dev tasks — primarily unit-test generation — runnable against readily-available local models with no custom training.

## Design / Architecture
- **gRPC service** — A Go gRPC server defined in protobuf exposes `ProcessRequest` (request/response messages for AI requests). Client/server stubs are generated from the `.proto`.
- **Semantic smart router** — Routing uses embedding similarity rather than prompt-based classification (local models proved unreliable at classifying with formatted output). A small local embedding model (`nomic-embed-text` via Ollama) embeds the request; `SelectProvider` compares it via cosine similarity against categorized example phrases in `prototypes.yaml` (50+ examples spanning Local/Cloud, expanded using a local LLM to generate phrasing variations). A similarity threshold (~0.35) triggers fallback to the cloud model when confidence is low. Users can "retry with a more powerful model."
- **Local model integration** — Code generation uses a local model (Qwen3-coder) via Ollama. A `test/sandbox` Calculator app with a "live fire" harness validates real generation end-to-end.
- **Agentic self-correction loop** — A Coordinator pattern decouples "thinking" (handler) from "doing" (file I/O, test execution). `CodeGenerator` (then `UnitTestHandler`) generates and exposes a `Fix(ctx, code, errorMsg)` method; a `Validator` runs `go test -c` and captures stderr; `GenerationCoordinator` orchestrates the Generate → Write → Validate → Fix retry loop. This was added because one-shot generation produced unused imports and markdown artifacts.
- **IDE abstraction layer** — Fully decoupled, communicates with the Go backend over gRPC. A VS Code extension (TypeScript stubs) provides a sidebar chat interface via a `WebviewViewProvider`/`ChatProvider`; a Zed extension (Rust/Wasm stubs) was scaffolded.

## Key Behaviors / Capabilities
- Best-guess routing of requests to local vs. cloud models, with user-driven retry on a more powerful model.
- Local-model task targets: unit-test generation (primary), code formatting/linting, commit-message generation, dependency analysis, docstring/comment generation.
- Self-correcting unit-test generation that auto-fixes compile errors (e.g., unused imports) until the code builds and passes.
- Sidebar chat in VS Code-compatible editors over gRPC.

## Notable Decisions / Constraints
- Embeddings-based routing chosen deliberately over prompt-based classification for reliability with local models.
- Cloud is the low-confidence fallback target; threshold ~0.35.
- Integration tests intentionally avoid exact prototype strings and include ambiguous queries to exercise fallback.
- Designed for extensibility — easy addition of new models and new IDE targets.
- Deferred (vNext): embedding `llama.cpp` for a self-contained app (no Ollama dependency), and adding IDE-gathered contextual information to the router.
