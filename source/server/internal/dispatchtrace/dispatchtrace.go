// Package dispatchtrace records an opt-in diagnostic trace of the first agentic
// dispatch from a selected parent conversation. Content is restricted to local
// owner-only files, never emitted by this package to ordinary logs. It records
// adapter inputs, not final provider wire serialization. Images and opaque
// reasoning are omitted. Prompt/tool content is otherwise verbatim and MAY
// contain secrets: this is not a general-purpose secret scrubber. Transport
// headers, provider config objects, and raw transport errors are not captured.
package dispatchtrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/llm"
)

// EnableEnv opts in via environment; ParentEnv must also match.
// Alternatively an owner-only armed file selects the parent at runtime.
const EnableEnv = "CERCANO_DISPATCH_TRACE"

// ParentEnv selects the parent conversation whose first agentic dispatch is captured.
const ParentEnv = "CERCANO_DISPATCH_TRACE_PARENT"

// DirEnv overrides the trace output directory (tests, or a workspace-local
// location). Empty uses DefaultDir().
const DirEnv = "CERCANO_DISPATCH_TRACE_DIR"

// Enabled reports environment opt-in (the live arming file is checked separately).
func Enabled() bool { return os.Getenv(EnableEnv) == "1" }

// DefaultDir is the default restricted output directory.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cercano", "trace", "dispatch")
}

func outputDir() string {
	if d := os.Getenv(DirEnv); d != "" {
		return d
	}
	return DefaultDir()
}

// Trace records one dispatch's diagnostic trace. A nil *Trace is valid and
// every method is a no-op, so callers wire unconditionally and pay nothing
// when tracing is off.
type Trace struct {
	mu       sync.Mutex
	f        *os.File
	enc      *json.Encoder
	seq      int
	dispatch string
}

type record struct {
	Time     time.Time `json:"ts"`
	Seq      int       `json:"seq"`
	Kind     string    `json:"kind"`
	Dispatch string    `json:"dispatch_id"`
	Iter     int       `json:"iteration,omitempty"`
	Event    any       `json:"event"`
}

// Begin starts a trace for one dispatch. It returns nil — a fully functional
// no-op — unless the local operator enabled tracing via EnableEnv. Trace
// setup failures (unwritable dir) also degrade to nil rather than failing the
// dispatch: diagnostics must never break work.
func Begin(dispatchID, conversationID string) *Trace {
	dir := outputDir()
	if !selected(dir, conversationID) {
		return nil
	}
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	// Refuse existing loose permissions and symlink leaves; do not chmod a
	// directory the operator may be using for something else. Use a trusted
	// owner-controlled parent directory (same-user filesystem races are outside
	// this diagnostic facility's threat boundary).
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		log.Print("[dispatch-trace] refused unsafe output directory")
		return nil
	}
	// Persistent, exclusive claim bounds collection to ONE dispatch, even across
	// worker processes or restarts. A fresh output directory explicitly rearms.
	claim, err := os.OpenFile(filepath.Join(dir, ".claimed"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil
	}
	_ = claim.Close()
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.jsonl", sanitizeID(dispatchID), time.Now().UnixNano()))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil
	}
	// Explicit chmod: the process umask may already be tighter, and the file
	// MUST be owner-only regardless.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil
	}
	t := &Trace{f: f, enc: json.NewEncoder(f), dispatch: dispatchID}
	log.Printf("[dispatch-trace] capture opened: dispatch=%s", sanitizeID(dispatchID))
	t.write(0, "dispatch_open", map[string]string{
		"dispatch_id":     dispatchID,
		"conversation_id": conversationID,
		"format":          "1",
	})
	return t
}

// selected supports explicit environment opt-in or a private live arming file.
// The latter is checked per dispatch so a warm agent need not restart merely
// to enable diagnostics. The persistent claim still limits both paths to one.
func selected(dir, parent string) bool {
	if parent == "" || dir == "" {
		return false
	}
	if Enabled() {
		return os.Getenv(ParentEnv) == parent
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	path := filepath.Join(dir, "armed")
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 256 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 257))
	return err == nil && len(data) <= 256 && strings.TrimSpace(string(data)) == parent
}

// sanitizeID keeps the dispatch id out of path traversal territory (sub-agent
// conversation ids are minted hex, but the trace must be safe by construction).
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "dispatch"
	}
	return b.String()
}

// write emits one JSONL record. Best-effort: an encoding error is swallowed
// (a broken trace must never fail the dispatch).
func (t *Trace) write(iter int, kind string, ev any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enc == nil {
		return
	}
	t.seq++
	if err := t.enc.Encode(record{Time: time.Now().UTC(), Seq: t.seq, Kind: kind, Dispatch: t.dispatch, Iter: iter, Event: ev}); err != nil {
		log.Print("[dispatch-trace] write failed; capture disabled")
		_ = t.f.Close()
		t.f = nil
		t.enc = nil
		return
	}
}

// Close flushes and closes the trace file. Nil-safe.
func (t *Trace) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f != nil {
		_ = t.f.Close()
		t.f = nil
		t.enc = nil
	}
}

// --- context plumbing -------------------------------------------------------

type traceKey struct{}

// WithTrace stamps the trace onto ctx so the tool loop, compaction passes and
// summarizer seam can record without any new parameter threading.
func WithTrace(ctx context.Context, t *Trace) context.Context {
	return context.WithValue(ctx, traceKey{}, t)
}

// From reads the trace stamped on ctx (nil when absent or disabled).
func From(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

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
func (t *Trace) DispatchStart(ev DispatchStartEvent) {
	t.write(0, "dispatch_start", ev)
}

// DispatchDoneEvent closes the trace with the loop's accounting. Err is the
// safe failure code, empty on success.
type DispatchDoneEvent struct {
	Err          string   `json:"error,omitempty"`
	Iterations   int      `json:"iterations,omitempty"`
	InputTokens  int      `json:"input_tokens,omitempty"`
	OutputTokens int      `json:"output_tokens,omitempty"`
	CalledTools  []string `json:"called_tools,omitempty"`
}

// DispatchDone records the dispatch outcome.
func (t *Trace) DispatchDone(ev DispatchDoneEvent) {
	t.write(0, "dispatch_done", ev)
}

// Note records an out-of-band observation (e.g. a degraded result) correlated
// to the dispatch.
func (t *Trace) Note(kind, text string) {
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
func (t *Trace) ModelRequest(iter int, provider string, req llm.ChatRequest, budget BudgetView) {
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
func (t *Trace) ModelResponse(iter int, provider, model string, resp llm.ChatResponse, err error) {
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
// delta the pass billed (summarizer spend).
func (t *Trace) Compaction(iter int, before, after []llm.Message, spentTokens int) {
	if t == nil {
		return
	}
	t.write(iter, "compaction", map[string]any{
		"messages_before": len(before),
		"messages_after":  len(after),
		"history_before":  sanitizeMessages(before),
		"history_after":   sanitizeMessages(after),
		"spent_tokens":    spentTokens,
	})
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
func (t *Trace) SummarizerRequest(ev SummarizerRequestEvent) {
	t.write(ev.Iteration, "summarizer_request", ev)
}

// SummarizerResponseEvent records what the summarizer returned.
type SummarizerResponseEvent struct {
	Route          string `json:"route"`
	RequestID      string `json:"request_id,omitempty"`
	Model          string `json:"model,omitempty"`
	Output         string `json:"output,omitempty"`
	InputTokens    int    `json:"input_tokens,omitempty"`
	OutputTokens   int    `json:"output_tokens,omitempty"`
	Err            string `json:"error,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Iteration      int    `json:"iteration,omitempty"`
}

// SummarizerResponse records the raw summarizer output (pre-parse) and the
// provider-reported usage, when the runner reports it.
func (t *Trace) SummarizerResponse(ev SummarizerResponseEvent) {
	t.write(ev.Iteration, "summarizer_response", ev)
}

type toolCallEvent struct {
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	Args      string `json:"args"` // raw JSON arguments exactly as emitted by the model
}

// ToolCall records a requested tool call in the model's requested order.
func (t *Trace) ToolCall(iter int, toolUseID, toolName, args string) {
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
func (t *Trace) ToolResult(iter int, toolUseID, toolName, content string, isError bool, originalBytes int, truncated bool) {
	t.write(iter, "tool_result", toolResultEvent{
		ToolUseID: toolUseID, ToolName: toolName, Content: content,
		IsError: isError, Truncated: truncated, OriginalBytes: originalBytes,
	})
}

// --- sanitization -----------------------------------------------------------

// imageOmissionMarker documents an elided image without recording its bytes.
func imageOmissionMarker(b llm.Block) string {
	return fmt.Sprintf("[dispatchtrace: image omitted (media_type=%q, %d base64 bytes not recorded)]", b.MediaType, len(b.ImageData))
}

func reasoningOmissionMarker(b llm.Block) string {
	return fmt.Sprintf("[dispatchtrace: reasoning blob omitted (%d bytes not recorded; id=%q)]", len(b.ReasoningData), b.ReasoningID)
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
