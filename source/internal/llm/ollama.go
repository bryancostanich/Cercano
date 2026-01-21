package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"cercano/source/internal/router"
)

type OllamaProvider struct {
	ModelName string
	BaseURL   string
	Client    *http.Client
}

func NewOllamaProvider(modelName, baseURL string) *OllamaProvider {
	return &OllamaProvider{
		ModelName: modelName,
		BaseURL:   baseURL,
		Client:    http.DefaultClient,
	}
}

func (p *OllamaProvider) Name() string {
	return p.ModelName
}

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
}

func (p *OllamaProvider) Process(ctx context.Context, req *router.Request) (*router.Response, error) {
	url := fmt.Sprintf("%s/api/generate", p.BaseURL)

	payload := generateRequest{
		Model:  p.ModelName,
		Prompt: req.Input,
		Stream: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error: %s", string(respBody))
	}

	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &router.Response{Output: genResp.Response}, nil
}
