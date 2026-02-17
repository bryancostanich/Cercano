package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"

	"gopkg.in/yaml.v3"
)

// Ollama API structs
type ollamaGenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options"`
}

type ollamaGenerateResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

type ollamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Prototypes represents the categorized example phrases from YAML.
type Prototypes struct {
	LocalModel []string `yaml:"local_model"`
	CloudModel []string `yaml:"cloud_model"`
}

// PrototypeEmbedding stores a phrase and its pre-calculated embedding.
type PrototypeEmbedding struct {
	Phrase    string
	Embedding []float64
	Category  string
}

// Request represents a request to be processed by an AI model.
type Request struct {
	Input          string
	ProviderConfig *ProviderConfig
}

// ProviderConfig represents a cloud provider configuration.
type ProviderConfig struct {
	Provider string
	Model    string
	ApiKey   string
}

// Response represents a response from an AI model.
type Response struct {
	Output string
}

// ModelProvider defines the interface for an AI model provider (local or cloud).
type ModelProvider interface {
	Process(ctx context.Context, req *Request) (*Response, error)
	Name() string
}

// Router defines the interface for a smart router that selects a model provider.
type Router interface {
	SelectProvider(req *Request) (ModelProvider, error)
}

// CloudFactory defines a function that creates a Cloud Model Provider.
type CloudFactory func(ctx context.Context, provider, model, apiKey string) (ModelProvider, error)

const (
	ollamaAPIURL          = "http://localhost:11434/api/generate"
	ollamaEmbeddingAPIURL = "http://localhost:11434/api/embeddings"
	similarityThreshold   = 0.45
)

// SmartRouter implements the Router interface with routing logic based on semantic similarity.
type SmartRouter struct {
	ModelProviders     map[string]ModelProvider
	EmbeddingModelName string
	Prototypes         []PrototypeEmbedding
	httpClient         *http.Client
	CloudFactory       CloudFactory
}

// NewSmartRouter creates a new SmartRouter, loads prototypes, and pre-calculates their embeddings.
func NewSmartRouter(local, cloud ModelProvider, embeddingModel string, client *http.Client, prototypesPath string, cloudFactory CloudFactory) (*SmartRouter, error) {
	if client == nil {
		client = http.DefaultClient
	}

	yamlBytes, err := ioutil.ReadFile(prototypesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load prototypes from %s: %w", prototypesPath, err)
	}

	var rawPrototypes Prototypes
	if err := yaml.Unmarshal(yamlBytes, &rawPrototypes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal prototypes: %w", err)
	}

	sr := &SmartRouter{
		ModelProviders: map[string]ModelProvider{
			"LocalModel": local,
			"CloudModel": cloud,
		},
		EmbeddingModelName: embeddingModel,
		httpClient:         client,
		CloudFactory:       cloudFactory,
	}

	// Pre-calculate embeddings for all prototypes
	for _, phrase := range rawPrototypes.LocalModel {
		embedding, err := sr.GetEmbedding(phrase)
		if err != nil {
			return nil, fmt.Errorf("failed to get embedding for local prototype '%s': %w", phrase, err)
		}
		sr.Prototypes = append(sr.Prototypes, PrototypeEmbedding{
			Phrase:    phrase,
			Embedding: embedding,
			Category:  "LocalModel",
		})
	}

	for _, phrase := range rawPrototypes.CloudModel {
		embedding, err := sr.GetEmbedding(phrase)
		if err != nil {
			return nil, fmt.Errorf("failed to get embedding for cloud prototype '%s': %w", phrase, err)
		}
		sr.Prototypes = append(sr.Prototypes, PrototypeEmbedding{
			Phrase:    phrase,
			Embedding: embedding,
			Category:  "CloudModel",
		})
	}

	return sr, nil
}

// GetEmbedding calls Ollama's embedding API to get a vector representation of the text.
func (sr *SmartRouter) GetEmbedding(text string) ([]float64, error) {
	requestBody, err := json.Marshal(ollamaEmbeddingRequest{
		Model:  sr.EmbeddingModelName,
		Prompt: text,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	req, err := http.NewRequest("POST", ollamaEmbeddingAPIURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create new HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sr.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to Ollama API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("Ollama API returned non-OK status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var embeddingResp ollamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embeddingResp); err != nil {
		return nil, fmt.Errorf("failed to decode Ollama API response: %w", err)
	}

	return embeddingResp.Embedding, nil
}

// CosineSimilarity calculates the cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// SelectProvider implements the smart routing algorithm using semantic similarity.
func (sr *SmartRouter) SelectProvider(req *Request) (ModelProvider, error) {
	// If the request specifies a cloud provider config, use it directly.
	if req.ProviderConfig != nil && sr.CloudFactory != nil {
		fmt.Printf("Router: Using cloud provider from config: %s\n", req.ProviderConfig.Provider)
		// We use context.Background() here for provider initialization if needed,
		// or we could pass the request context if we change the interface.
		// For now, let's use Background.
		return sr.CloudFactory(context.Background(), req.ProviderConfig.Provider, req.ProviderConfig.Model, req.ProviderConfig.ApiKey)
	}

	embedding, err := sr.GetEmbedding(req.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding for request: %w", err)
	}

	var bestCategory string
	var maxSimilarity float64 = -1.0
	var bestPhrase string

	for _, proto := range sr.Prototypes {
		sim := CosineSimilarity(embedding, proto.Embedding)
		if sim > maxSimilarity {
			maxSimilarity = sim
			bestCategory = proto.Category
			bestPhrase = proto.Phrase
		}
	}

	fmt.Printf("Router Decision: %s | Similarity: %.4f | Closest Prototype: '%s'\n", bestCategory, maxSimilarity, bestPhrase)

	// Fallback logic
	if maxSimilarity < similarityThreshold {
		fmt.Printf("Similarity below threshold (%.2f). Defaulting to CloudModel.\n", similarityThreshold)
		if provider, ok := sr.ModelProviders["CloudModel"]; ok {
			return provider, nil
		}
	}

	if provider, ok := sr.ModelProviders[bestCategory]; ok {
		return provider, nil
	}

	return nil, fmt.Errorf("could not determine classification for input: '%s'", req.Input)
}

