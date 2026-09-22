// Package dispatchhistory preserves automatic append-only evidence for agentic
// dispatches in the host-owned conversation database. No arming files, process
// environment switches, per-parent selection, or one-shot claims are used.
package dispatchhistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

type Sink func(context.Context, conversation.DispatchEvent) error

type Recorder struct {
	mu       sync.Mutex
	ctx      context.Context
	sink     Sink
	dispatch string
	seq      int64
	closed   bool
	failures int
}

// Begin is automatic whenever dispatch conversation persistence is available.
// The host acknowledges each write; failures are metadata-only logged and
// exposed to the dispatch result. Evidence must not fail the user's task.
func Begin(ctx context.Context, dispatchID string, sink Sink) *Recorder {
	if sink == nil {
		return nil
	}
	return &Recorder{ctx: ctx, sink: sink, dispatch: dispatchID}
}

func (t *Recorder) write(iter int, kind string, event any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.seq++
	data, err := json.Marshal(event)
	if err == nil {
		ctx, cancel := context.WithTimeout(t.ctx, 5*time.Second)
		err = t.sink(ctx, conversation.DispatchEvent{ConversationID: t.dispatch, Seq: t.seq, Kind: kind, Iteration: iter, Timestamp: time.Now().UTC(), PayloadJSON: string(data)})
		cancel()
	}
	if err != nil {
		t.failures++
		log.Printf("[dispatch-history] persistence failed: dispatch=%s seq=%d kind=%s error=%s", t.dispatch, t.seq, kind, ErrorCode(err))
	}
}
func (t *Recorder) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
}
func (t *Recorder) Failures() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failures
}

type recorderKey struct{}

func WithRecorder(ctx context.Context, t *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, t)
}
func From(ctx context.Context) *Recorder { t, _ := ctx.Value(recorderKey{}).(*Recorder); return t }

// --- event payloads ---------------------------------------------------------

// DispatchStartEvent is the dispatch's opening record: what was asked, on
// what route, with what toolset and limits.
type DispatchStartEvent struct {
	Mode               string   `json:"mode,omitempty"`
	Task               string   `json:"task,omitempty"`
	WorkDir            string   `json:"work_dir,omitempty"`
	ParentConversation string   `json:"parent_conversation_id,omitempty"`
	Provider           string   `json:"provider,omitempty"`
	Model              string   `json:"model,omitempty"`
	Tier               string   `json:"tier,omitempty"`
	FallbackTier       string   `json:"fallback_tier,omitempty"`
	IsCloud            bool     `json:"is_cloud,omitempty"`
	GrantedTools       []string `json:"granted_tools,omitempty"`
	IgnoredTools       []string `json:"ignored_tools,omitempty"`
	MaxIterations      int      `json:"max_iterations,omitempty"`
	TokenBudget        int      `json:"token_budget,omitempty"`
	ContextWindow      int      `json:"context_window,omitempty"`
	ContextWindowKnown bool     `json:"context_window_known,omitempty"`
}

// DispatchStart records the dispatch's configuration and task.
func (t *Recorder) DispatchStart(ev DispatchStartEvent) {
	t.write(0, "dispatch_start", ev)
}

// DispatchDoneEvent closes the trace with the loop's accounting. Err is the
// safe failure code, empty on success.
type DispatchDoneEvent struct {
	Err                 string   `json:"error,omitempty"`
	PersistenceFailures int      `json:"persistence_failures,omitempty"`
	Iterations          int      `json:"iterations,omitempty"`
	InputTokens         int      `json:"input_tokens,omitempty"`
	OutputTokens        int      `json:"output_tokens,omitempty"`
	CalledTools         []string `json:"called_tools,omitempty"`
}

// DispatchDone records the dispatch outcome.
func (t *Recorder) DispatchDone(ev DispatchDoneEvent) {
	ev.PersistenceFailures = t.Failures()
	t.write(0, "dispatch_done", ev)
}

// Note records an out-of-band observation (e.g. a degraded result) correlated
// to the dispatch.
func (t *Recorder) Note(kind, text string) {
	t.write(0, "note", map[string]string{"note_kind": kind, "text": text})
}

// BudgetView is the request-budget accounting around one model call.
type BudgetView struct {
	MessageTokens          int  `json:"message_tokens,omitempty"`
	SystemTokens           int  `json:"system_tokens,omitempty"`
	ToolTokens             int  `json:"tool_schema_tokens,omitempty"`
	OutputReserve          int  `json:"output_reserve_tokens,omitempty"`
	EstimatedRequestTokens int  `json:"estimated_request_tokens,omitempty"`
	ContextWindow          int  `json:"context_window,omitempty"`
	ContextWindowKnown     bool `json:"context_window_known,omitempty"`
	PromptBudget           int  `json:"prompt_budget,omitempty"`
	TrimmedMessages        int  `json:"trimmed_messages,omitempty"`
}

type toolView struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Permission  llm.Permission  `json:"permission,omitempty"`
}

type modelRequestEvent struct {
	Provider        string         `json:"provider"`
	Model           string         `json:"model,omitempty"`
	Tier            string         `json:"tier,omitempty"`
	FallbackTier    string         `json:"fallback_tier,omitempty"`
	System          string         `json:"system,omitempty"`
	Messages        []llm.Message  `json:"messages"`
	Tools           []toolView     `json:"tools,omitempty"`
	ToolChoice      llm.ToolChoice `json:"tool_choice"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	DisableThinking bool           `json:"disable_thinking,omitempty"`
	RequestID       string         `json:"request_id,omitempty"`
	Budget          BudgetView     `json:"budget"`
}

// ModelRequest records the exact model-facing ChatRequest (post-compaction,
// post-trim, pre-provider-serialization) plus the budget accounting that
// produced it.
func (t *Recorder) ModelRequest(iter int, provider string, req llm.ChatRequest, budget BudgetView) {
	if t == nil {
		return
	}
	tools := make([]toolView, len(req.Tools))
	for i, tl := range req.Tools {
		tools[i] = toolView{Name: tl.Name, Description: tl.Description, Schema: tl.Schema, Permission: tl.Permission}
	}
	t.write(iter, "model_request", modelRequestEvent{
		Provider:        provider,
		Model:           req.Model,
		Tier:            req.Tier,
		FallbackTier:    req.FallbackTier,
		System:          req.System,
		Messages:        sanitizeMessages(req.Messages),
		Tools:           tools,
		ToolChoice:      req.ToolChoice,
		ConversationID:  req.ConversationID,
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		DisableThinking: req.DisableThinking,
		RequestID:       req.RequestID,
		Budget:          budget,
	})
}

type routeView struct {
	Provider       string `json:"provider,omitempty"`
	Profile        string `json:"profile,omitempty"`
	Destination    string `json:"destination,omitempty"`
	Model          string `json:"model,omitempty"`
	ContextWindow  int    `json:"context_window,omitempty"`
	ContextKnown   bool   `json:"context_window_known,omitempty"`
	SupportsVision bool   `json:"supports_vision,omitempty"`
	VisionKnown    bool   `json:"vision_known,omitempty"`
}

func routeViewOf(r *llm.ServingRoute) *routeView {
	if r == nil {
		return nil
	}
	return &routeView{
		Provider: r.Provider, Profile: r.Profile, Destination: r.Destination,
		Model: r.Model, ContextWindow: r.ContextWindow, ContextKnown: r.ContextWindowKnown,
		SupportsVision: r.SupportsVision, VisionKnown: r.VisionKnown,
	}
}

type modelResponseEvent struct {
	Provider     string         `json:"provider"`
	Model        string         `json:"model,omitempty"` // the model that actually served the call
	StopReason   string         `json:"stop_reason,omitempty"`
	Usage        llm.TokenUsage `json:"usage,omitempty"`
	InputTokens  int            `json:"input_tokens,omitempty"`
	OutputTokens int            `json:"output_tokens,omitempty"`
	Blocks       []llm.Block    `json:"blocks"`
	Route        *routeView     `json:"route,omitempty"`
	Error        string         `json:"error,omitempty"`
}

// ModelResponse records the aggregated provider response — finish/stop reason,
// normalized usage, the actual serving route, and the exact returned blocks.
// err is recorded alongside so failed/partial responses are visible.
func (t *Recorder) ModelResponse(iter int, provider, model string, resp llm.ChatResponse, err error) {
	if t == nil {
		return
	}
	ev := modelResponseEvent{
		Provider:     provider,
		Model:        model,
		StopReason:   resp.StopReason,
		Usage:        resp.Usage,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
		Blocks:       sanitizeBlocks(resp.Blocks),
		Route:        routeViewOf(resp.Route),
	}
	if err != nil {
		ev.Error = ErrorCode(err)
	}
	t.write(iter, "model_response", ev)
}

// Compaction records history before and after ONE inline compaction pass
// within the dispatch, correlated by iteration. spentTokens is the budget
// delta (reported tokens plus explicitly attributed fallback estimates).
type CompactionAccounting struct {
	ReportedTokens  int `json:"reported_tokens"`
	EstimatedTokens int `json:"estimated_tokens"`
}

func (t *Recorder) Compaction(iter int, before, after []llm.Message, spentTokens int, accounting ...CompactionAccounting) {
	if t == nil {
		return
	}
	payload := map[string]any{
		"messages_before": len(before),
		"messages_after":  len(after),
		"history_before":  sanitizeMessages(before),
		"history_after":   sanitizeMessages(after),
		"spent_tokens":    spentTokens,
	}
	if len(accounting) > 0 {
		payload["usage_accounting"] = accounting[0]
	}
	t.write(iter, "compaction", payload)
}

// SummarizerRequestEvent records one compaction-summarizer model call.
type SummarizerRequestEvent struct {
	Route          string   `json:"route"` // "local" | "cloud"
	RequestID      string   `json:"request_id,omitempty"`
	Model          string   `json:"model,omitempty"` // empty = runner default
	Tier           string   `json:"tier,omitempty"`
	MaxTokens      int      `json:"max_tokens,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	Prompt         string   `json:"prompt"` // the exact prompt text sent
	ConversationID string   `json:"conversation_id,omitempty"`
	Iteration      int      `json:"iteration,omitempty"`
}

// SummarizerRequest records the exact prompt handed to the summarizer.
func (t *Recorder) SummarizerRequest(ev SummarizerRequestEvent) {
	t.write(ev.Iteration, "summarizer_request", ev)
}

// SummarizerResponseEvent records what the summarizer returned.
type SummarizerResponseEvent struct {
	Route                 string `json:"route"`
	RequestID             string `json:"request_id,omitempty"`
	Model                 string `json:"model,omitempty"`
	Output                string `json:"output,omitempty"`
	InputTokens           int    `json:"input_tokens,omitempty"`
	OutputTokens          int    `json:"output_tokens,omitempty"`
	Err                   string `json:"error,omitempty"`
	ConversationID        string `json:"conversation_id,omitempty"`
	Iteration             int    `json:"iteration,omitempty"`
	EstimatedInputTokens  int    `json:"estimated_input_tokens,omitempty"`
	EstimatedOutputTokens int    `json:"estimated_output_tokens,omitempty"`
}

// SummarizerResponse records the raw summarizer output (pre-parse) and the
// provider-reported usage, when the runner reports it.
func (t *Recorder) SummarizerResponse(ev SummarizerResponseEvent) {
	t.write(ev.Iteration, "summarizer_response", ev)
}

type toolCallEvent struct {
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	Args      string `json:"args"` // raw JSON arguments exactly as emitted by the model
}

// ToolCall records a requested tool call in the model's requested order.
func (t *Recorder) ToolCall(iter int, toolUseID, toolName, args string) {
	t.write(iter, "tool_call", toolCallEvent{ToolUseID: toolUseID, ToolName: toolName, Args: args})
}

type toolResultEvent struct {
	ToolUseID     string `json:"tool_use_id"`
	ToolName      string `json:"tool_name"`
	Content       string `json:"content"` // the exact model-facing content, truncation marker included
	IsError       bool   `json:"is_error,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	OriginalBytes int    `json:"original_bytes,omitempty"` // pre-truncation size when truncation applied
}

// ToolResult records a tool execution outcome in completion order. Content is
// what the model sees; when the window cap fired, the content already carries
// the truncation marker and truncated reports it.
func (t *Recorder) ToolResult(iter int, toolUseID, toolName, content string, isError bool, originalBytes int, truncated bool) {
	t.write(iter, "tool_result", toolResultEvent{
		ToolUseID: toolUseID, ToolName: toolName, Content: content,
		IsError: isError, Truncated: truncated, OriginalBytes: originalBytes,
	})
}

// --- sanitization -----------------------------------------------------------

// imageOmissionMarker documents an elided image without recording its bytes.
func imageOmissionMarker(b llm.Block) string {
	return fmt.Sprintf("[dispatchhistory: image omitted (media_type=%q, %d base64 bytes not recorded)]", b.MediaType, len(b.ImageData))
}

func reasoningOmissionMarker(b llm.Block) string {
	return fmt.Sprintf("[dispatchhistory: reasoning blob omitted (%d bytes not recorded; id=%q)]", len(b.ReasoningData), b.ReasoningID)
}

// sanitizeBlocks deep-copies blocks, replacing image bytes / URLs and opaque
// provider reasoning blobs with omission markers. Everything else — text,
// tool args, tool results, error flags — is recorded verbatim: it is exactly
// what the model received, which is the point of the trace.
func sanitizeBlocks(blocks []llm.Block) []llm.Block {
	if blocks == nil {
		return nil
	}
	out := make([]llm.Block, len(blocks))
	copy(out, blocks)
	for i := range out {
		out[i].ToolInput = append(json.RawMessage(nil), out[i].ToolInput...)
	}
	for i := range out {
		b := &out[i]
		switch b.Type {
		case llm.BlockImage:
			b.Text = imageOmissionMarker(*b)
			b.ImageData = ""
			b.ImageURL = ""
		case llm.BlockReasoning:
			b.Text = reasoningOmissionMarker(*b)
			b.ReasoningData = ""
		}
	}
	return out
}

// sanitizeMessages applies sanitizeBlocks to a whole history.
func sanitizeMessages(msgs []llm.Message) []llm.Message {
	if msgs == nil {
		return nil
	}
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llm.Message{Role: m.Role, Blocks: sanitizeBlocks(m.Blocks)}
	}
	return out
}

// ErrorCode never serializes arbitrary transport errors (which may contain
// credentials, request URLs or response bodies).
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, io.EOF):
		return "eof"
	default:
		return "error"
	}
}

// Snapshot preserves pre-pass evidence without retaining mutable block slices.
func Snapshot(msgs []llm.Message) []llm.Message {
	if msgs == nil {
		return nil
	}
	out := append([]llm.Message(nil), msgs...)
	for i := range out {
		out[i].Blocks = append([]llm.Block(nil), msgs[i].Blocks...)
		for j := range out[i].Blocks {
			out[i].Blocks[j].ToolInput = append(json.RawMessage(nil), msgs[i].Blocks[j].ToolInput...)
		}
	}
	return out
}
