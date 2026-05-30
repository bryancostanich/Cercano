package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolSchema describes a tool to the local LLM. Mirrors the JSON-schema shape
// expected by Ollama's /api/chat `tools` parameter.
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Tool is one capability the local LLM can invoke during a dispatch loop.
type Tool interface {
	Name() string
	Schema() ToolSchema
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is a lookup table of Tools by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds t to the registry; returns an error if the name is taken.
func (r *Registry) Register(t Tool) error {
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

// Get returns the Tool for the given name and whether it was found.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Schemas returns a slice of ToolSchema for every registered tool.
// Order is unspecified.
func (r *Registry) Schemas() []ToolSchema {
	out := make([]ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Schema())
	}
	return out
}
