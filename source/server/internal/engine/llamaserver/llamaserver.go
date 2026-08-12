package llamaserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/localruntime"
)

const runtimeName = "llama_server"

// Engine implements InferenceEngine against a supervised llama-server sidecar.
// Process lifecycle stays with localruntime.Manager; this adapter only resolves
// a ready localhost endpoint and speaks llama-server's OpenAI-compatible API.
type Engine struct {
	Client  *http.Client
	Manager localruntime.Manager
}

func NewEngine(manager localruntime.Manager) *Engine {
	return &Engine{
		Client:  &http.Client{Timeout: 10 * time.Minute},
		Manager: manager,
	}
}

func (e *Engine) Name() string { return runtimeName }

func (e *Engine) Complete(ctx context.Context, model, prompt, systemPrompt string, opts engine.GenOptions) (engine.CompletionResult, error) {
	var messages []openAIMessage
	if systemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: prompt})
	resp, err := e.chat(ctx, model, messages, nil, opts, false, nil)
	if err != nil {
		return engine.CompletionResult{}, err
	}
	return engine.CompletionResult{
		Output:       resp.Content,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}

func (e *Engine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, opts engine.GenOptions, onToken func(string)) (engine.CompletionResult, error) {
	var messages []openAIMessage
	if systemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: prompt})
	resp, err := e.chat(ctx, model, messages, nil, opts, true, onToken)
	if err != nil {
		return engine.CompletionResult{}, err
	}
	return engine.CompletionResult{
		Output:       resp.Content,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}

func (e *Engine) ChatWithTools(ctx context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	messages := make([]openAIMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		messages = append(messages, openAIMessageFromEngine(msg))
	}
	model := req.Model
	resp, err := e.chat(ctx, model, messages, req.Tools, engine.GenOptions{MaxTokens: req.MaxTokens}, false, nil)
	if err != nil {
		return engine.ChatResponse{}, err
	}
	return engine.ChatResponse{
		Content:      resp.Content,
		ToolCalls:    resp.ToolCalls,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}

// Embed implements engine.EmbeddingService against a warm llama-server
// embedding instance. endpointFor reuses a resident instance for the model
// (spawning one in --embedding mode on first use — argsFor adds the flag for
// encoder GGUFs), so embeddings follow the configured runtime instead of
// silently requiring an Ollama daemon.
func (e *Engine) Embed(ctx context.Context, model, text string) ([]float64, error) {
	endpoint, resolvedModel, _, err := e.endpointFor(ctx, model)
	if err != nil {
		return nil, err
	}
	if resolvedModel == "" {
		resolvedModel = "default"
	}
	payload, err := json.Marshal(embeddingsRequest{Model: resolvedModel, Input: text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(endpoint, "/")+"/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama-server embeddings error (status %d): %s", resp.StatusCode, string(body))
	}
	var decoded embeddingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) == 0 {
		return nil, errors.New("llama-server embeddings response contained no data")
	}
	return decoded.Data[0].Embedding, nil
}

type embeddingsRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func (e *Engine) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	if e.Manager == nil {
		return nil, errors.New("llama-server runtime manager is not configured")
	}
	models, err := e.Manager.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]engine.ModelInfo, 0, len(models))
	for _, model := range models {
		if model.Runtime != runtimeName {
			continue
		}
		modified := ""
		if !model.ModifiedAt.IsZero() {
			modified = model.ModifiedAt.Format(time.RFC3339)
		}
		out = append(out, engine.ModelInfo{
			Name:       model.DisplayName,
			Size:       model.SizeBytes,
			ModifiedAt: modified,
		})
	}
	return out, nil
}

type chatResult struct {
	Content      string
	ToolCalls    []engine.ToolCall
	InputTokens  int
	OutputTokens int
}

func (e *Engine) chat(ctx context.Context, model string, messages []openAIMessage, tools []engine.ToolSchemaJSON, opts engine.GenOptions, stream bool, onToken func(string)) (chatResult, error) {
	endpoint, resolvedModel, _, err := e.endpointFor(ctx, model)
	if err != nil {
		return chatResult{}, err
	}
	if resolvedModel == "" {
		resolvedModel = "default"
	}
	payload := chatCompletionRequest{
		Model:           resolvedModel,
		Messages:        messages,
		Tools:           tools,
		Stream:          stream,
		ParseToolCalls:  len(tools) > 0,
		ParallelToolUse: len(tools) > 0,
		Temperature:     opts.Temperature,
		MaxTokens:       engine.EffectiveMaxTokens(opts.MaxTokens),
	}
	if len(tools) > 0 {
		payload.ToolChoice = "auto"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return chatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(endpoint, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return chatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return chatResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return chatResult{}, fmt.Errorf("llama-server chat error (status %d): %s", resp.StatusCode, string(body))
	}
	if stream {
		return decodeStreamingChat(resp.Body, onToken)
	}
	return decodeChatResponse(resp.Body)
}

func (e *Engine) endpointFor(ctx context.Context, requested string) (endpoint string, modelName string, supportsVision bool, err error) {
	if e.Manager == nil {
		return "", "", false, errors.New("llama-server runtime manager is not configured")
	}
	models, inventoryErr := e.Manager.Inventory(ctx)
	selected := matchRuntimeModel(requested, models)
	if inventoryErr != nil && selected.ID == "" {
		return "", "", false, inventoryErr
	}
	modelID := requested
	if selected.ID != "" {
		modelID = selected.ID
	}
	instances, err := e.Manager.Instances(ctx)
	if err != nil {
		return "", "", false, err
	}
	startingID := ""
	for _, instance := range instances {
		if instance.Runtime != runtimeName || instance.Endpoint == "" {
			continue
		}
		if modelID != "" && instance.ModelID != modelID && instance.ModelID != requested {
			continue
		}
		switch instance.State {
		case localruntime.InstanceRunning, localruntime.InstanceHealthy:
			return instance.Endpoint, modelNameForRequest(selected, requested), selected.SupportsVision, nil
		case localruntime.InstanceStarting:
			// Still loading the model — its port isn't open yet, so a
			// request now would get connection-refused. Wait for it below
			// instead of racing it (or worse, spawning a second copy).
			startingID = instance.ID
		}
	}
	if startingID != "" {
		endpoint, err := e.awaitInstanceReady(ctx, startingID)
		if err != nil {
			return "", "", false, err
		}
		return endpoint, modelNameForRequest(selected, requested), selected.SupportsVision, nil
	}
	start, err := e.Manager.Start(ctx, localruntime.StartRequest{
		Runtime: runtimeName,
		ModelID: requested,
	})
	if err != nil {
		return "", "", false, err
	}
	if start.Endpoint == "" {
		return "", "", false, errors.New("llama-server started without an endpoint")
	}
	return start.Endpoint, modelNameForRequest(selected, requested), selected.SupportsVision, nil
}

// awaitInstanceReady blocks until a still-loading instance becomes usable,
// bounded by the caller's context. The provider keeps a slow initial load
// alive past its readiness window (finishReadiness flips it to running), so
// callers that can afford to wait get the warm instance instead of an error.
func (e *Engine) awaitInstanceReady(ctx context.Context, instanceID string) (string, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		instances, err := e.Manager.Instances(ctx)
		if err != nil {
			return "", err
		}
		found := false
		for _, instance := range instances {
			if instance.ID != instanceID {
				continue
			}
			found = true
			switch instance.State {
			case localruntime.InstanceRunning, localruntime.InstanceHealthy:
				return instance.Endpoint, nil
			case localruntime.InstanceStarting:
				// keep waiting
			default:
				reason := instance.LastError
				if reason == "" {
					reason = instance.State.String()
				}
				return "", fmt.Errorf("llama-server instance failed while loading: %s", reason)
			}
			break
		}
		if !found {
			return "", errors.New("llama-server instance disappeared while loading")
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("llama-server is still loading the model: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (e *Engine) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

func matchRuntimeModel(requested string, models []localruntime.ModelRecord) localruntime.ModelRecord {
	for _, model := range models {
		if model.Runtime != runtimeName {
			continue
		}
		if requested == "" && model.Active {
			return model
		}
		// Shared matcher — MUST stay in lockstep with the provider's
		// resolveModel. A name the provider resolves but this misses makes
		// endpointFor skip every warm instance and spawn a fresh
		// llama-server per request until physical RAM is gone.
		if localruntime.MatchesModel(requested, model) {
			return model
		}
	}
	if requested == "" {
		for _, model := range models {
			if model.Runtime == runtimeName {
				return model
			}
		}
	}
	return localruntime.ModelRecord{}
}

func modelNameForRequest(model localruntime.ModelRecord, requested string) string {
	if model.ID != "" {
		return model.ID
	}
	if requested != "" {
		return requested
	}
	return "default"
}

type chatCompletionRequest struct {
	Model           string                  `json:"model"`
	Messages        []openAIMessage         `json:"messages"`
	Tools           []engine.ToolSchemaJSON `json:"tools,omitempty"`
	ToolChoice      string                  `json:"tool_choice,omitempty"`
	Stream          bool                    `json:"stream"`
	ParseToolCalls  bool                    `json:"parse_tool_calls,omitempty"`
	ParallelToolUse bool                    `json:"parallel_tool_calls,omitempty"`
	// Temperature is a pointer so 0 (greedy decoding) survives serialization
	// instead of being dropped as a zero value; nil keeps the server default.
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (f *openAIToolFunction) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Name = raw.Name
	f.Arguments = normalizeToolArguments(raw.Arguments)
	return nil
}

func openAIMessageFromEngine(msg engine.ChatMessage) openAIMessage {
	out := openAIMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
		Name:       msg.Name,
	}
	for _, tc := range msg.ToolCalls {
		args := "{}"
		if len(tc.Function.Arguments) > 0 {
			args = string(tc.Function.Arguments)
		}
		out.ToolCalls = append(out.ToolCalls, openAIToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: openAIToolFunction{
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(strconvQuote(args)),
			},
		})
	}
	return out
}

func strconvQuote(value string) string {
	quoted, _ := json.Marshal(value)
	return string(quoted)
}

type chatCompletionResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Timings struct {
		PromptN    int `json:"prompt_n"`
		PredictedN int `json:"predicted_n"`
	} `json:"timings"`
}

func decodeChatResponse(r io.Reader) (chatResult, error) {
	var resp chatCompletionResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return chatResult{}, err
	}
	if len(resp.Choices) == 0 {
		return chatResult{}, errors.New("llama-server response contained no choices")
	}
	message := resp.Choices[0].Message
	return chatResult{
		Content:      message.Content,
		ToolCalls:    toolCallsFromOpenAI(message.ToolCalls),
		InputTokens:  firstNonZero(resp.Usage.PromptTokens, resp.Timings.PromptN),
		OutputTokens: firstNonZero(resp.Usage.CompletionTokens, resp.Timings.PredictedN),
	}, nil
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta openAIMessage `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Timings struct {
		PromptN    int `json:"prompt_n"`
		PredictedN int `json:"predicted_n"`
	} `json:"timings"`
}

func decodeStreamingChat(r io.Reader, onToken func(string)) (chatResult, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out strings.Builder
	var inputTokens, outputTokens int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return chatResult{}, err
		}
		inputTokens = firstNonZero(inputTokens, chunk.Usage.PromptTokens, chunk.Timings.PromptN)
		outputTokens = firstNonZero(outputTokens, chunk.Usage.CompletionTokens, chunk.Timings.PredictedN)
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			out.WriteString(choice.Delta.Content)
			if onToken != nil {
				onToken(choice.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return chatResult{}, err
	}
	return chatResult{
		Content:      out.String(),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

func toolCallsFromOpenAI(calls []openAIToolCall) []engine.ToolCall {
	out := make([]engine.ToolCall, 0, len(calls))
	for i, call := range calls {
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("tc_%d", i)
		}
		out = append(out, engine.ToolCall{
			ID: id,
			Function: engine.ToolCallFunc{
				Name:      call.Function.Name,
				Arguments: normalizeToolArguments(call.Function.Arguments),
			},
		})
	}
	return out
}

func normalizeToolArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return json.RawMessage("{}")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			s = strings.TrimSpace(s)
			if s == "" {
				return json.RawMessage("{}")
			}
			return json.RawMessage(s)
		}
	}
	return raw
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
