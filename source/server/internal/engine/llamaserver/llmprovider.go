package llamaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cercano/source/server/internal/crashlog"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/failurelog"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/localruntime"
)

// LLMProvider adapts the llama-server runtime to the native inference.Provider
// interface. Each call resolves (and warms, when needed) the runtime instance
// serving the requested model via endpointFor, then delegates the chat to an
// OpenAI-compatible client pointed at that instance — llama-server exposes
// /v1/chat/completions natively. This is what lets the dispatch engine's open
// lane (watchdog checks, coproc capabilities, sub-agent loops under a local
// locus) follow the configured runtime instead of silently requiring an
// Ollama daemon.
type LLMProvider struct {
	eng *Engine
}

// NewLLMProvider wraps eng as a native inference.Provider.
func NewLLMProvider(eng *Engine) *LLMProvider { return &LLMProvider{eng: eng} }

func (p *LLMProvider) Name() string { return "llama_server" }

func (p *LLMProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}

func (p *LLMProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c, req, err := p.clientFor(ctx, req)
	if err != nil {
		p.logProviderFailure(ctx, req, localHTTPDiagnostic{}, err)
		return llm.ChatResponse{}, err
	}
	resp, err := c.Chat(ctx, req)
	if err != nil {
		p.logProviderFailure(ctx, req, localHTTPDiagnostic{}, err)
		return llm.ChatResponse{}, err
	}
	return resp, nil
}

func (p *LLMProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	c, req, err := p.clientFor(ctx, req)
	if err != nil {
		p.logProviderFailure(ctx, req, localHTTPDiagnostic{}, err)
		return nil, err
	}
	stream, err := c.StreamChat(ctx, req)
	if err != nil {
		p.logProviderFailure(ctx, req, localHTTPDiagnostic{}, err)
		return nil, err
	}
	return stream, nil
}

// clientFor resolves the instance endpoint for req.Model and returns a client
// bound to it, plus the request rewritten to the resolved model id (llama-server
// serves one model per instance; the id in the request is informational).
func (p *LLMProvider) clientFor(ctx context.Context, req llm.ChatRequest) (*openai.Client, llm.ChatRequest, error) {
	endpoint, model, supportsVision, err := p.eng.endpointFor(ctx, req.Model)
	if err != nil {
		return nil, req, err
	}
	if model == "" {
		model = "default"
	}
	req.Model = model
	if req.MaxTokens <= 0 {
		req.MaxTokens = engine.DefaultMaxTokens
	}
	// SupportsVision reflects the resolved model's real capability: true only
	// for a vision model launched with its mmproj (endpointFor derives it from
	// the resolved ModelRecord). A text-only or mmproj-less backend reports
	// false, so image blocks are stripped rather than sent to a server that
	// would 500 on "image input is not supported".
	c := openai.NewClient(openai.Config{
		BaseURL:        strings.TrimRight(endpoint, "/") + "/v1",
		Model:          model,
		Backend:        "llama_server",
		SupportsVision: supportsVision,
		OnHTTPError: func(ctx context.Context, diag openai.HTTPErrorDiagnostic) {
			p.logProviderFailure(ctx, req, localHTTPDiagnostic{
				Method:        diag.Method,
				Path:          diag.Path,
				StatusCode:    diag.StatusCode,
				ContentLength: diag.ContentLength,
				Body:          diag.Body,
				TransportErr:  diag.TransportErr,
			}, nil)
		},
	})
	return c, req, nil
}

type localHTTPDiagnostic struct {
	Method        string
	Path          string
	StatusCode    int
	ContentLength int64
	Body          string
	TransportErr  string
}

func (p *LLMProvider) logProviderFailure(ctx context.Context, req llm.ChatRequest, httpDiag localHTTPDiagnostic, err error) {
	if p == nil || p.eng == nil || p.eng.failureWriter() == nil {
		return
	}
	budget := estimateRequestBudget(req)
	fields := failurelog.Event{
		"scope":                          "provider",
		"provider":                       "llama_server",
		"model":                          req.Model,
		"conversation_id":                req.ConversationID,
		"request_id":                     req.RequestID,
		"request_token_estimate":         budget.EstimatedTotal,
		"system_tokens":                  budget.SystemTokens,
		"message_tokens":                 budget.MessageTokens,
		"tool_schema_tokens":             budget.ToolSchemaTokens,
		"output_reserve":                 budget.OutputReserve,
		"estimated_total_request_tokens": budget.EstimatedTotal,
		"max_tokens":                     req.MaxTokens,
	}
	if cw := p.eng.contextWindow(req.Model); cw > 0 {
		fields["context_window"] = cw
	}
	if httpDiag.StatusCode != 0 {
		fields["http_status"] = httpDiag.StatusCode
	}
	if httpDiag.ContentLength != 0 {
		fields["http_content_length"] = httpDiag.ContentLength
	}
	if httpDiag.Method != "" {
		fields["http_method"] = httpDiag.Method
	}
	if httpDiag.Path != "" {
		fields["http_path"] = httpDiag.Path
	}
	if httpDiag.Body != "" {
		fields["http_body"] = failurelog.SanitizeMessage(httpDiag.Body)
	}
	if httpDiag.TransportErr != "" {
		fields["transport_error"] = failurelog.SanitizeMessage(httpDiag.TransportErr)
	}
	if err != nil {
		fields["error_class"] = string(llm.ClassOf(err))
		fields["message"] = failurelog.SanitizeMessage(err.Error())
	} else {
		fields["error_class"] = string(classifyHTTPFailure(httpDiag))
		fields["message"] = failurelog.SanitizeMessage(firstNonEmpty(httpDiag.Body, httpDiag.TransportErr))
	}
	p.enrichRuntimeDiagnostics(ctx, req.Model, fields)
	p.eng.failureWriter().Log("local_runtime.provider_error", fields)
}

func (p *LLMProvider) enrichRuntimeDiagnostics(ctx context.Context, model string, fields failurelog.Event) {
	if p == nil || p.eng == nil || p.eng.Manager == nil {
		return
	}
	instances, _ := p.eng.Manager.Instances(ctx)
	var chosen localruntime.InstanceRecord
	for _, inst := range instances {
		if inst.Runtime != runtimeName {
			continue
		}
		if model == "" || inst.ModelID == model || strings.Contains(model, inst.ModelID) || strings.Contains(inst.ModelID, model) {
			chosen = inst
			break
		}
		if chosen.ID == "" {
			chosen = inst
		}
	}
	if chosen.ID != "" {
		fields["instance_id"] = chosen.ID
		fields["instance_state"] = chosen.State.String()
		fields["pid"] = chosen.PID
		fields["port"] = chosen.Port
		fields["endpoint"] = chosen.Endpoint
		if chosen.LastError != "" {
			fields["instance_last_error"] = failurelog.SanitizeMessage(chosen.LastError)
		}
		if slots := fetchSlotsSnapshot(ctx, chosen.Endpoint); slots != "" {
			fields["slots"] = slots
		}
	}
	if logs, err := p.eng.Manager.Logs(ctx, localruntime.LogRequest{Tail: 20}); err == nil && len(logs) > 0 {
		fields["runtime_log_tail"] = compactRuntimeLogs(logs, chosen.ID)
	}
	if events := recentCrashlogRuntimeEvents(chosen.ID); len(events) > 0 {
		fields["recent_lifecycle_events"] = events
	}
}

type requestBudgetEstimate struct {
	SystemTokens     int
	MessageTokens    int
	ToolSchemaTokens int
	OutputReserve    int
	EstimatedTotal   int
}

func estimateRequestBudget(req llm.ChatRequest) requestBudgetEstimate {
	budget := requestBudgetEstimate{
		SystemTokens:  estimateTokens(req.System),
		OutputReserve: req.MaxTokens,
	}
	for _, msg := range req.Messages {
		for _, block := range msg.Blocks {
			budget.MessageTokens += estimateTokens(block.Text) + estimateTokens(block.Content) + estimateTokens(block.ImageURL) + estimateTokens(block.ImageData)
		}
	}
	if tools, err := json.Marshal(req.Tools); err == nil {
		budget.ToolSchemaTokens = estimateTokens(string(tools))
	}
	budget.EstimatedTotal = budget.SystemTokens + budget.MessageTokens + budget.ToolSchemaTokens + budget.OutputReserve
	return budget
}

func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

func compactRuntimeLogs(logs []localruntime.LogEntry, instanceID string) []map[string]any {
	out := make([]map[string]any, 0, len(logs))
	for _, entry := range logs {
		if instanceID != "" && entry.RuntimeID != "" && entry.RuntimeID != instanceID {
			continue
		}
		row := map[string]any{
			"source":  entry.Source,
			"level":   entry.Level,
			"message": failurelog.SanitizeMessage(entry.Message),
		}
		if !entry.Timestamp.IsZero() {
			row["ts"] = entry.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		if entry.RuntimeID != "" {
			row["runtime_id"] = entry.RuntimeID
		}
		out = append(out, row)
	}
	if len(out) > 10 {
		out = out[len(out)-10:]
	}
	return out
}

func recentCrashlogRuntimeEvents(instanceID string) []map[string]any {
	entries, err := crashlog.TailEntries(defaultCrashLogPath(), 40)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, 10)
	for _, e := range entries {
		if e.Kind != crashlog.KindRuntimeEvent || e.Runtime == nil || e.Runtime.Runtime != runtimeName {
			continue
		}
		if instanceID != "" && e.Runtime.InstanceID != "" && e.Runtime.InstanceID != instanceID {
			continue
		}
		row := map[string]any{
			"ts":     e.Timestamp.UTC().Format(time.RFC3339Nano),
			"event":  e.Event,
			"reason": failurelog.SanitizeMessage(e.Reason),
		}
		if e.Runtime.InstanceID != "" {
			row["instance_id"] = e.Runtime.InstanceID
		}
		if e.Runtime.PID != 0 {
			row["pid"] = e.Runtime.PID
		}
		if e.Runtime.Port != 0 {
			row["port"] = e.Runtime.Port
		}
		if e.Extra != nil {
			if v, ok := e.Extra["escalated"]; ok {
				row["escalated"] = v
			}
			if v, ok := e.Extra["wait_ms"]; ok {
				row["wait_ms"] = v
			}
		}
		out = append(out, row)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func defaultCrashLogPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "cercano", "crash.log")
	}
	return "crash.log"
}

func fetchSlotsSnapshot(ctx context.Context, endpoint string) string {
	if endpoint == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/slots", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ""
	}
	if len(raw) > 4000 {
		return string(raw[:4000]) + "…"
	}
	return string(raw)
}

func classifyHTTPFailure(diag localHTTPDiagnostic) llm.ErrorClass {
	if diag.TransportErr != "" {
		return llm.ErrNetwork
	}
	if overflow, _, _ := llm.DetectContextOverflow(diag.Body); overflow {
		return llm.ErrContextOverflow
	}
	switch {
	case diag.StatusCode == http.StatusUnauthorized || diag.StatusCode == http.StatusForbidden:
		return llm.ErrAuth
	case diag.StatusCode == http.StatusTooManyRequests:
		return llm.ErrBusy
	case diag.StatusCode >= 500:
		return llm.ErrBusy
	case diag.StatusCode >= 400:
		return llm.ErrInvalidRequest
	default:
		return llm.ErrUnknown
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
