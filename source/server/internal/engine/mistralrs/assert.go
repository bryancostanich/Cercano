package mistralrs

import (
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
)

// Compile-time proof the mistral.rs engine adapter satisfies the seams main.go
// wires it into: the open InferenceEngine lane and the llm.Provider surface.
// A signature drift is caught here rather than at the wiring site.
var (
	_ engine.InferenceEngine = (*Engine)(nil)
	_ llm.Provider           = (*LLMProvider)(nil)
)
