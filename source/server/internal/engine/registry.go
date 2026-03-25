package engine

import (
	"fmt"
	"sync"
)

// EngineRegistry stores registered InferenceEngines and EmbeddingServices.
type EngineRegistry struct {
	mu        sync.RWMutex
	engines   map[string]InferenceEngine
	embedders map[string]EmbeddingService
}

// NewEngineRegistry creates a new EngineRegistry.
func NewEngineRegistry() *EngineRegistry {
	return &EngineRegistry{
		engines:   make(map[string]InferenceEngine),
		embedders: make(map[string]EmbeddingService),
	}
}

// RegisterEngine adds an InferenceEngine to the registry.
func (r *EngineRegistry) RegisterEngine(engine InferenceEngine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.engines[engine.Name()] = engine
}

// RegisterEmbedder adds an EmbeddingService to the registry.
func (r *EngineRegistry) RegisterEmbedder(embedder EmbeddingService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.embedders[embedder.Name()] = embedder
}

// GetEngine returns an InferenceEngine by name, or an error if not found.
func (r *EngineRegistry) GetEngine(name string) (InferenceEngine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	engine, ok := r.engines[name]
	if !ok {
		return nil, fmt.Errorf("engine %q not found in registry", name)
	}
	return engine, nil
}

// GetEmbedder returns an EmbeddingService by name, or an error if not found.
func (r *EngineRegistry) GetEmbedder(name string) (EmbeddingService, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	embedder, ok := r.embedders[name]
	if !ok {
		return nil, fmt.Errorf("embedder %q not found in registry", name)
	}
	return embedder, nil
}

// ListEngines returns a list of all registered InferenceEngine names.
func (r *EngineRegistry) ListEngines() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name := range r.engines {
		names = append(names, name)
	}
	return names
}

// ListEmbedders returns a list of all registered EmbeddingService names.
func (r *EngineRegistry) ListEmbedders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name := range r.embedders {
		names = append(names, name)
	}
	return names
}
