// Package inference defines the single provider seam every model-serving
// backend implements and every consumer (router, coordinator, chat, compaction,
// recap, research) depends on. It is the greenfield "one seam" the runtime work
// converged on: a request enters agnostic of the backend, flows through tier
// selection, and only touches a concrete backend at assembly in main.go.
//
// The seam speaks inference vocabulary — Infer / Stream over a Call, returning a
// Result — layered over the chat message model that stays in the llm package
// (Message, Block, Role*, Tool). Call / Result / Stream are aliases of the llm
// envelope types so this package names the seam without forking the chat
// vocabulary; consumers say inference.Provider and inference.Call while the
// field types (llm.Message, llm.Block, …) remain where they describe chat
// structure.
package inference

import (
	"context"

	"cercano/source/server/internal/llm"
)

// Capabilities describes what an inference provider supports, so consumers can
// adapt (tool use, parallel tools, prompt caching, vision, tool-count limits)
// without knowing which backend answers. Alias of llm.Capabilities (which is
// where the struct lives to avoid an import cycle — inference imports llm).
type Capabilities = llm.Capabilities

// Call is one inference request. Alias of the llm chat envelope: chat is one
// shape of inference, and the request carries the chat message model (System,
// Messages, Tools, …). Kept as an alias so the ~130 existing construction sites
// keep compiling while the seam is named in inference vocabulary.
type Call = llm.ChatRequest

// Result is one inference response (blocks, stop reason, token counts, the
// model that actually served). Alias of the llm chat response envelope.
type Result = llm.ChatResponse

// Stream is a streaming inference reader. Alias of the llm stream reader.
type Stream = llm.StreamReader

// Provider is THE inference seam. Every backend (local runtimes via their
// engine adapters, cloud vendors) implements it; every tier wrapper
// (inference.Open, inference.Cloud, inference.Failover) is itself a Provider;
// the router selects a Provider and callers invoke it without knowing the
// backend.
//
// The methods keep the names Chat / StreamChat deliberately. "Infer / Stream"
// would read more neutrally, but the Ollama Go SDK exposes an identically-named
// Chat method on a DIFFERENT type in this same codebase, so a blanket
// Chat→Infer rename is a real footgun for zero architectural gain. The seam's
// identity is its type (inference.Provider) and package, not the verb spelling.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Chat(ctx context.Context, req Call) (Result, error)
	StreamChat(ctx context.Context, req Call) (Stream, error)
}
