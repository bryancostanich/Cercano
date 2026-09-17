package openai

// This is an opt-in experiment seam, not a production reasoning fix. Callers
// must replay recorded tool results; attaching it to a normal tool loop does
// NOT disable tool execution. No headers are captured or credentials fetched.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

type ReasoningDiagnosticMode string

const (
	ReasoningDrop     ReasoningDiagnosticMode = "drop"
	ReasoningPreserve ReasoningDiagnosticMode = "preserve"
)

// BaseURL must match the selected client's base URL, without credentials or
// query parameters. Limits are mandatory. Timeout bounds the entire session.
type ReasoningDiagnosticConfig struct {
	ReasoningEffort              string // Empty leaves the endpoint default unchanged; diagnostic-only.
	Mode                         ReasoningDiagnosticMode
	BaseURL, Model               string
	MaxRequests                  int
	MaxBodyBytes, MaxMemoryBytes int64
	Timeout                      time.Duration
}

// Captures contain sensitive prompt/response material. Never log them or send
// them to another provider. They exclude HTTP headers, not secrets a prompt or
// endpoint may itself contain. Copies returned by Captures belong to the caller.
type ReasoningDiagnosticCapture struct {
	ReasoningEvidence                   ReasoningEvidence
	Request, Response                   []byte
	ReasoningArrived, ReasoningReplayed bool
	FinishReason                        string
}

type diagnosticCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type capturedContinuation struct {
	call         diagnosticCall
	count, index int
	reasoning    string
	batch        int
}
type reasoningDiagnosticKey struct{}

type ReasoningDiagnostic struct {
	mu            sync.Mutex
	cfg           ReasoningDiagnosticConfig
	endpoint      string
	deadline      time.Time
	busy, closed  bool
	requests      int
	used          int64
	continuations map[string]capturedContinuation
	captures      []ReasoningDiagnosticCapture
}

func NewReasoningDiagnostic(cfg ReasoningDiagnosticConfig) (*ReasoningDiagnostic, error) {
	switch cfg.ReasoningEffort {
	case "", "none", "low", "medium", "high":
	default:
		return nil, errors.New("reasoning diagnostic: unsupported reasoning effort")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("reasoning diagnostic: invalid base URL")
	}
	if (cfg.Mode != ReasoningDrop && cfg.Mode != ReasoningPreserve) || cfg.Model == "" || cfg.MaxRequests <= 0 || cfg.MaxRequests > 1000 || cfg.MaxBodyBytes <= 0 || cfg.MaxBodyBytes > 64<<20 || cfg.MaxMemoryBytes < cfg.MaxBodyBytes || cfg.MaxMemoryBytes > 512<<20 || cfg.Timeout <= 0 || cfg.Timeout > time.Hour {
		return nil, errors.New("reasoning diagnostic: invalid mode or finite bounds")
	}
	return &ReasoningDiagnostic{cfg: cfg, endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions", deadline: time.Now().Add(cfg.Timeout), continuations: make(map[string]capturedContinuation)}, nil
}

// WithReasoningDiagnostic explicitly opts this context into buffered capture.
// Use a separate session for each arm; never share it across conversations.
func WithReasoningDiagnostic(ctx context.Context, s *ReasoningDiagnostic) context.Context {
	if s == nil {
		panic("nil reasoning diagnostic")
	}
	return context.WithValue(ctx, reasoningDiagnosticKey{}, s)
}

func (s *ReasoningDiagnostic) Captures() []ReasoningDiagnosticCapture {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ReasoningDiagnosticCapture, len(s.captures))
	for i, c := range s.captures {
		out[i] = c
		out[i].Request = bytes.Clone(c.Request)
		out[i].Response = bytes.Clone(c.Response)
	}
	return out
}

// Close releases retained material. Go does not guarantee secure heap erasure.
// An in-flight request is not cancelled; it cannot commit capture after Close.
func (s *ReasoningDiagnostic) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.captures = nil
	s.continuations = nil
	s.used = 0
}

func diagnosticReadBody(body io.ReadCloser, limit int64) ([]byte, error) {
	if body == nil {
		return nil, errors.New("reasoning diagnostic: missing body")
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, errors.New("reasoning diagnostic: body read failed")
	}
	if int64(len(b)) > limit {
		return nil, errors.New("reasoning diagnostic: body limit exceeded")
	}
	return b, nil
}

func (s *ReasoningDiagnostic) do(req *http.Request, next goopenai.HTTPDoer) (resp *http.Response, err error) {
	s.mu.Lock()
	if s.closed || s.busy || s.requests >= s.cfg.MaxRequests || !time.Now().Before(s.deadline) {
		s.mu.Unlock()
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, errors.New("reasoning diagnostic: closed, busy, or exhausted")
	}
	s.busy = true
	s.requests++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		if err != nil {
			s.closed = true
		}
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithDeadline(req.Context(), s.deadline)
	defer cancel()
	if ctx.Err() != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, ctx.Err()
	}
	if req.Method != http.MethodPost || req.URL.String() != s.endpoint {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, errors.New("reasoning diagnostic: endpoint changed")
	}
	b, err := diagnosticReadBody(req.Body, s.cfg.MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	// Bound the body before the ordinary temperature patch reads it; capture the
	// patched bytes actually sent over the wire, including explicit zero.
	cloned := req.Clone(ctx)
	cloned.Body = io.NopCloser(bytes.NewReader(b))
	cloned, err = patchExplicitZeroTemperature(cloned)
	if err != nil {
		return nil, errors.New("reasoning diagnostic: temperature patch failed")
	}
	b, err = diagnosticReadBody(cloned.Body, s.cfg.MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil {
		return nil, errors.New("reasoning diagnostic: invalid request")
	}
	var model string
	var stream bool
	n := 1
	if json.Unmarshal(obj["model"], &model) != nil || model != s.cfg.Model || json.Unmarshal(obj["stream"], &stream) != nil || !stream {
		return nil, errors.New("reasoning diagnostic: model changed or nonstream request")
	}
	if raw, ok := obj["n"]; ok {
		if json.Unmarshal(raw, &n) != nil || n != 1 {
			return nil, errors.New("reasoning diagnostic: multiple choices unsupported")
		}
	}
	if s.cfg.ReasoningEffort != "" {
		if raw, exists := obj["reasoning_effort"]; exists {
			var effort string
			if json.Unmarshal(raw, &effort) != nil || effort != s.cfg.ReasoningEffort {
				return nil, errors.New("reasoning diagnostic: reasoning effort changed")
			}
		}
		obj["reasoning_effort"], _ = json.Marshal(s.cfg.ReasoningEffort)
		b, err = json.Marshal(obj)
		if err != nil {
			return nil, errors.New("reasoning diagnostic: encoding failed")
		}
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(obj["messages"], &messages) != nil {
		return nil, errors.New("reasoning diagnostic: invalid messages")
	}
	replayed := false
	seen := map[string]bool{}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("reasoning diagnostic: closed")
	}
	for _, m := range messages {
		if _, ok := m["reasoning_content"]; ok {
			s.mu.Unlock()
			return nil, errors.New("reasoning diagnostic: preexisting reasoning unsupported")
		}
		raw, ok := m["tool_calls"]
		if !ok {
			continue
		}
		var role string
		var calls []diagnosticCall
		if json.Unmarshal(m["role"], &role) != nil || role != "assistant" || json.Unmarshal(raw, &calls) != nil || len(calls) == 0 {
			s.mu.Unlock()
			return nil, errors.New("reasoning diagnostic: invalid assistant calls")
		}
		var captured capturedContinuation
		for i, c := range calls {
			if c.ID == "" || seen[c.ID] {
				s.mu.Unlock()
				return nil, errors.New("reasoning diagnostic: duplicate or empty call ID")
			}
			seen[c.ID] = true
			if s.cfg.Mode == ReasoningPreserve {
				v, ok := s.continuations[c.ID]
				if !ok || v.reasoning == "" || v.call != c || v.count != len(calls) || v.index != i || (i > 0 && v.batch != captured.batch) {
					s.mu.Unlock()
					return nil, errors.New("reasoning diagnostic: missing or inconsistent continuation")
				}
				captured = v
			}
		}
		if s.cfg.Mode == ReasoningPreserve {
			m["reasoning_content"], _ = json.Marshal(captured.reasoning)
			replayed = true
		}
	}
	s.mu.Unlock()
	if replayed {
		obj["messages"], _ = json.Marshal(messages)
		b, err = json.Marshal(obj)
		if err != nil {
			return nil, errors.New("reasoning diagnostic: encoding failed")
		}
	}
	if int64(len(b)) > s.cfg.MaxBodyBytes {
		return nil, errors.New("reasoning diagnostic: body limit exceeded")
	}
	s.mu.Lock()
	remaining := s.cfg.MaxMemoryBytes - s.used - int64(len(b))
	s.mu.Unlock()
	if remaining <= 0 {
		return nil, errors.New("reasoning diagnostic: memory limit exceeded")
	}
	cloned.Body = io.NopCloser(bytes.NewReader(b))
	cloned.ContentLength = int64(len(b))
	cloned.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	// A diagnostic must not follow redirects: even a same-host redirect changes
	// the route and may leak captured prompts. Copy, never mutate, the client.
	if client, ok := next.(*http.Client); ok {
		copyClient := *client
		copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		next = &copyClient
	}
	resp, err = next.Do(cloned)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, errors.New("reasoning diagnostic: transport failed")
	}
	limit := s.cfg.MaxBodyBytes
	if remaining < limit {
		limit = remaining
	}
	body, readErr := diagnosticReadBody(resp.Body, limit)
	if readErr != nil {
		return nil, readErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Never feed error bodies through normalizer logging in an opted-in session.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reasoning diagnostic: HTTP status %d", resp.StatusCode)
	}
	reason, ids, finish, err := parseDiagnosticSSE(body)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("reasoning diagnostic: closed")
	}
	added := int64(len(b) + len(body) + len(reason))
	for _, call := range ids {
		added += int64(len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments))
		if _, ok := s.continuations[call.ID]; ok {
			return nil, errors.New("reasoning diagnostic: reused response call ID")
		}
	}
	if added > s.cfg.MaxMemoryBytes-s.used {
		return nil, errors.New("reasoning diagnostic: memory limit exceeded")
	}
	for i, call := range ids {
		s.continuations[call.ID] = capturedContinuation{reasoning: reason, batch: s.requests, call: call, count: len(ids), index: i}
	}
	s.used += added
	s.captures = append(s.captures, ReasoningDiagnosticCapture{Request: b, Response: body, ReasoningArrived: reason != "", ReasoningReplayed: replayed, FinishReason: finish, ReasoningEvidence: diagnosticReasoningEvidence(body)})
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

func parseDiagnosticSSE(body []byte) (string, []diagnosticCall, string, error) {
	bad := func() (string, []diagnosticCall, string, error) {
		return "", nil, "", errors.New("reasoning diagnostic: malformed or incomplete single-choice SSE")
	}
	var reason strings.Builder
	calls := map[int]diagnosticCall{}
	done := false
	finish := ""
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		if done || !bytes.HasPrefix(line, []byte("data:")) {
			return bad()
		}
		data := bytes.TrimSpace(line[5:])
		if bytes.Equal(data, []byte("[DONE]")) {
			done = true
			continue
		}
		var event struct {
			Choices []struct {
				Index  int     `json:"index"`
				Finish *string `json:"finish_reason"`
				Delta  struct {
					Reason string `json:"reasoning_content"`
					Calls  []struct {
						Index *int `json:"index"`
						diagnosticCall
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &event) != nil || len(event.Choices) > 1 {
			return bad()
		}
		for _, c := range event.Choices {
			if c.Index != 0 || finish != "" {
				return bad()
			}
			reason.WriteString(c.Delta.Reason)
			for _, call := range c.Delta.Calls {
				if call.Index == nil || *call.Index < 0 {
					return bad()
				}
				v := calls[*call.Index]
				v.ID += call.ID
				v.Function.Name += call.Function.Name
				v.Function.Arguments += call.Function.Arguments
				calls[*call.Index] = v
			}
			if c.Finish != nil && *c.Finish != "" {
				finish = *c.Finish
			}
		}
	}
	if !done || finish == "" {
		return bad()
	}
	ids := make([]diagnosticCall, len(calls))
	seen := map[string]bool{}
	for i := range ids {
		id, ok := calls[i]
		if !ok || id.ID == "" || seen[id.ID] || id.Function.Name == "" || !json.Valid([]byte(id.Function.Arguments)) {
			return bad()
		}
		seen[id.ID] = true
		ids[i] = id
	}
	if (len(ids) > 0) != (finish == "tool_calls") {
		return bad()
	}
	return reason.String(), ids, finish, nil
}
