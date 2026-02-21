package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"sort"
	"strings"

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
	Intents struct {
		Coding []string `yaml:"coding"`
		Chat   []string `yaml:"chat"`
	} `yaml:"intents"`
	Providers struct {
		Local []string `yaml:"local"`
		Cloud []string `yaml:"cloud"`
	} `yaml:"providers"`
}

// PrototypeType defines the type of prototype (intent vs provider).
type PrototypeType string

const (
	TypeIntent   PrototypeType = "intent"
	TypeProvider PrototypeType = "provider"
)

// PrototypeEmbedding stores a phrase and its pre-calculated embedding.
type PrototypeEmbedding struct {
	Phrase    string
	Embedding []float64
	Category  string
	Type      PrototypeType
}

// Intent represents the user's intended task (e.g., coding or chat).
type Intent string

const (
	IntentCoding Intent = "coding"
	IntentChat   Intent = "chat"
)

// Request represents a request to be processed by an AI model.
type Request struct {
	Input          string
	ProviderConfig *ProviderConfig
	WorkDir        string
	FileName       string
}

// ProviderConfig represents a cloud provider configuration.
type ProviderConfig struct {
	Provider string
	Model    string
	ApiKey   string
}

// Response represents a response from an AI model.
type Response struct {
	Output           string
	FileChanges      []FileChange
	RoutingMetadata  RoutingMetadata
	ValidationErrors string // New field for rich feedback
}

// FileChange represents a change to a specific file.
type FileChange struct {
	Path    string
	Content string
	Action  string // "CREATE", "UPDATE", "DELETE"
}

// RoutingMetadata contains details about how the request was routed.
type RoutingMetadata struct {
	ModelName  string
	Confidence float64
	Escalated  bool
}

// ModelProvider defines the interface for an AI model provider (local or cloud).
type ModelProvider interface {
	Process(ctx context.Context, req *Request) (*Response, error)
	Name() string
}

// Router defines the interface for a smart router that selects a model provider.
type Router interface {
	SelectProvider(req *Request, intent Intent) (ModelProvider, error)
	ClassifyIntent(req *Request) (Intent, error)
	GetModelProviders() map[string]ModelProvider
}

// CloudFactory defines a function that creates a Cloud Model Provider.
type CloudFactory func(ctx context.Context, provider, model, apiKey string) (ModelProvider, error)

const (
	ollamaAPIURL          = "http://localhost:11434/api/generate"
	ollamaEmbeddingAPIURL = "http://localhost:11434/api/embeddings"
	similarityThreshold   = 0.50
	classificationTopK    = 3
)

// SmartRouter implements the Router interface with routing logic based on semantic similarity.
type SmartRouter struct {
	ModelProviders     map[string]ModelProvider
	EmbeddingModelName string
	IntentPrototypes   []PrototypeEmbedding
	ProviderPrototypes []PrototypeEmbedding
	httpClient         *http.Client
	CloudFactory       CloudFactory
}

func (sr *SmartRouter) GetModelProviders() map[string]ModelProvider {
	return sr.ModelProviders
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

	// Helper to pre-calculate embeddings
	getEmbeds := func(phrases []string, category string, pType PrototypeType) ([]PrototypeEmbedding, error) {
		var embeds []PrototypeEmbedding
		for _, phrase := range phrases {
			embedding, err := sr.GetEmbedding(phrase)
			if err != nil {
				return nil, fmt.Errorf("failed to get embedding for prototype '%s': %w", phrase, err)
			}
			embeds = append(embeds, PrototypeEmbedding{
				Phrase:    phrase,
				Embedding: embedding,
				Category:  category,
				Type:      pType,
			})
		}
		return embeds, nil
	}

	// Load Intent prototypes
	intentCoding, err := getEmbeds(rawPrototypes.Intents.Coding, "Intent:Coding", TypeIntent)
	if err != nil {
		return nil, err
	}
	sr.IntentPrototypes = append(sr.IntentPrototypes, intentCoding...)

	intentChat, err := getEmbeds(rawPrototypes.Intents.Chat, "Intent:Chat", TypeIntent)
	if err != nil {
		return nil, err
	}
	sr.IntentPrototypes = append(sr.IntentPrototypes, intentChat...)

	// Load Provider prototypes
	providerLocal, err := getEmbeds(rawPrototypes.Providers.Local, "Provider:Local", TypeProvider)
	if err != nil {
		return nil, err
	}
	sr.ProviderPrototypes = append(sr.ProviderPrototypes, providerLocal...)

	providerCloud, err := getEmbeds(rawPrototypes.Providers.Cloud, "Provider:Cloud", TypeProvider)
	if err != nil {
		return nil, err
	}
	sr.ProviderPrototypes = append(sr.ProviderPrototypes, providerCloud...)

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
		return nil, fmt.Errorf("failed to connect to Ollama API. Is Ollama running? Error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("embedding model '%s' not found. Please run 'ollama pull %s'", sr.EmbeddingModelName, sr.EmbeddingModelName)
	}

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

// contextDelimiter is the marker the VS Code extension uses to append file context.
const contextDelimiter = "--- Context from "

// extractQueryText strips appended file context from the input, returning only
// the user's query. This prevents source code context from skewing embeddings.
func extractQueryText(input string) string {
	if idx := strings.Index(input, contextDelimiter); idx > 0 {
		return strings.TrimSpace(input[:idx])
	}
	return input
}

// topKAveragePerCategory computes the mean of the top-K cosine similarities
// per category and returns the category with the highest average.
// Categories with fewer than K prototypes use all available.
func topKAveragePerCategory(queryEmbedding []float64, prototypes []PrototypeEmbedding, k int) (bestCategory string, bestAvg float64) {
	// Group similarities by category
	catSims := make(map[string][]float64)
	for _, proto := range prototypes {
		sim := CosineSimilarity(queryEmbedding, proto.Embedding)
		catSims[proto.Category] = append(catSims[proto.Category], sim)
	}

	bestAvg = -1.0
	for cat, sims := range catSims {
		// Sort ascending so top-K are at the end
		sort.Float64s(sims)
		n := k
		if n > len(sims) {
			n = len(sims)
		}
		var sum float64
		for i := len(sims) - n; i < len(sims); i++ {
			sum += sims[i]
		}
		avg := sum / float64(n)
		if avg > bestAvg {
			bestAvg = avg
			bestCategory = cat
		}
	}
	return
}

// ClassifyIntent determines if the user's request is a coding task or a chat task.
func (sr *SmartRouter) ClassifyIntent(req *Request) (Intent, error) {
	queryText := extractQueryText(req.Input)
	embedding, err := sr.GetEmbedding(queryText)
	if err != nil {
		return "", fmt.Errorf("failed to get embedding for request: %w", err)
	}

	bestCategory, bestAvg := topKAveragePerCategory(embedding, sr.IntentPrototypes, classificationTopK)

	// Default to Chat if similarity is low or ambiguous
	intent := IntentChat
	// Only promote to Coding if we are reasonably confident AND it's the clear winner
	if bestAvg >= similarityThreshold && bestCategory == "Intent:Coding" {
		intent = IntentCoding
	}

	if intent == IntentCoding {
		fmt.Printf("Intent Classification: %s | Top-%d Avg Similarity: %.4f | Category: %s\n", intent, classificationTopK, bestAvg, bestCategory)
	}
	return intent, nil
}

// SelectProvider implements the smart routing algorithm using semantic similarity.
func (sr *SmartRouter) SelectProvider(req *Request, intent Intent) (ModelProvider, error) {
	queryText := extractQueryText(req.Input)
	embedding, err := sr.GetEmbedding(queryText)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding for request: %w", err)
	}

	bestCategory, maxSimilarity := topKAveragePerCategory(embedding, sr.ProviderPrototypes, classificationTopK)

	fmt.Printf("Router Decision: %s | Top-%d Avg Similarity: %.4f\n", bestCategory, classificationTopK, maxSimilarity)

	// Determine final category (handling fallback)
	finalCategory := bestCategory
	if maxSimilarity < similarityThreshold {
		fmt.Printf("Similarity below threshold (%.2f). Defaulting based on intent: %s\n", similarityThreshold, intent)
		
		if intent == IntentCoding {
			finalCategory = "Provider:Cloud"
		} else {
			finalCategory = "Provider:Local"
		}
	}

	// If LocalModel is selected, return the local provider
	if finalCategory == "Provider:Local" {
		if provider, ok := sr.ModelProviders["LocalModel"]; ok {
			return provider, nil
		}
	}

	// If CloudModel is selected, check if we have a specific config from the client
	if req.ProviderConfig != nil && sr.CloudFactory != nil {
		fmt.Printf("Router: Using cloud provider from client config: %s\n", req.ProviderConfig.Provider)
		return sr.CloudFactory(context.Background(), req.ProviderConfig.Provider, req.ProviderConfig.Model, req.ProviderConfig.ApiKey)
	}

	// Otherwise use the default cloud provider
	if provider, ok := sr.ModelProviders["CloudModel"]; ok {
		return provider, nil
	}

	return nil, fmt.Errorf("could not determine classification for input: '%s'", req.Input)
}

