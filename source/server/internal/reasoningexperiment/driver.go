// Package reasoningexperiment runs opt-in, bounded reasoning-continuation trials.
// It has no tool executor, filesystem access, retry loop, or fallback provider.
package reasoningexperiment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/pkg/config"
)

const MaxInputBytes = 8 << 20

type RecordedResult struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Content   string          `json:"content"`
	IsError   bool            `json:"is_error,omitempty"`
}

type Spec struct {
	Profile        string           `json:"profile"`
	Model          string           `json:"model"`
	System         string           `json:"system"`
	Messages       []llm.Message    `json:"messages"`
	Tools          []llm.Tool       `json:"tools"`
	Results        []RecordedResult `json:"recorded_results"`
	MaxRequests    int              `json:"max_requests"`    // per arm
	MaxTokens      int              `json:"max_tokens"`      // per request, including provider reasoning
	TimeoutSeconds int              `json:"timeout_seconds"` // entire pair
	Temperature    *float64         `json:"temperature"`
	PreserveFirst  bool             `json:"preserve_first,omitempty"`
}

type Step struct {
	RequestHash           string         `json:"request_hash"`
	ResponseHash          string         `json:"response_hash"`
	NormalizedRequestHash string         `json:"normalized_request_hash"`
	BatchHash             string         `json:"batch_hash,omitempty"`
	ToolIDs               []string       `json:"tool_ids,omitempty"`
	ReasoningArrived      bool           `json:"reasoning_arrived"`
	ReasoningReplayed     bool           `json:"reasoning_replayed"`
	Finish                string         `json:"finish"`
	Usage                 llm.TokenUsage `json:"usage"`
	ServedModel           string         `json:"served_model,omitempty"`
}
type Arm struct {
	Mode   openai.ReasoningDiagnosticMode `json:"mode"`
	Status string                         `json:"status"`
	Steps  []Step                         `json:"steps"`
}
type Report struct {
	Profile                  string `json:"profile"`
	Model                    string `json:"model"`
	MaxRequestedOutputTokens int    `json:"max_requested_output_tokens"`
	Arms                     []Arm  `json:"arms"`
	// Same-index inputs compare only after removing reasoning_content. False
	// means the trajectories diverged; it is not evidence for a reasoning effect.
	ComparableInputs []bool `json:"comparable_inputs"`
}

// Service is an optional extension of host/worker provider resolvers.
type Service interface {
	RunReasoningDiagnostic(context.Context, Spec) (Report, error)
}

func Decode(r io.Reader) (Spec, error) {
	var s Spec
	b, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil || len(b) > MaxInputBytes {
		return s, errors.New("diagnostic input unreadable or exceeds 8 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&s); err != nil {
		return Spec{}, errors.New("invalid diagnostic input schema")
	}
	if d.Decode(new(any)) != io.EOF {
		return Spec{}, errors.New("diagnostic input must contain one JSON object")
	}
	return s, nil
}

func hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func canonical(b json.RawMessage) (string, error) {
	var v map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&v); err != nil || v == nil {
		return "", errors.New("arguments must be a JSON object")
	}
	if d.Decode(new(any)) != io.EOF {
		return "", errors.New("invalid arguments")
	}
	out, err := json.Marshal(v)
	return string(out), err
}
func key(name string, args json.RawMessage) (string, error) {
	a, e := canonical(args)
	return name + "\x00" + a, e
}

func validate(s Spec) (map[string]RecordedResult, error) {
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > MaxInputBytes || len(s.Tools) > 64 || len(s.Results) > 1024 {
		return nil, errors.New("diagnostic input exceeds finite size/count limits")
	}

	if s.Profile == "" || s.Model == "" || s.MaxRequests < 1 || s.MaxRequests > 12 || s.MaxTokens < 1 || s.MaxTokens > 32768 || 2*s.MaxRequests*s.MaxTokens > 262144 || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 600 || s.Temperature == nil || *s.Temperature < 0 || *s.Temperature > 2 || len(s.Messages) == 0 {
		return nil, errors.New("explicit profile, model, temperature, messages and finite bounds required (1–12 requests/arm, 1–32768 tokens/request, at most 262144 requested tokens/pair, 1–600 seconds/pair)")
	}
	// Old tool history cannot be reconstructed in preserve mode without its
	// captured reasoning. Reject before credentials or network access.
	for _, m := range s.Messages {
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant && m.Role != llm.RoleSystem {
			return nil, errors.New("unsupported history role")
		}
		for _, b := range m.Blocks {
			if b.Type != llm.BlockText {
				return nil, errors.New("initial history must be text-only; historical tool reasoning cannot be reconstructed")
			}
		}
	}
	names := map[string]bool{}
	for _, t := range s.Tools {
		if t.Name == "" || names[t.Name] || !json.Valid(t.Schema) {
			return nil, errors.New("invalid or duplicate tool definition")
		}
		names[t.Name] = true
	}
	results := map[string]RecordedResult{}
	for _, r := range s.Results {
		k, e := key(r.Name, r.Arguments)
		if e != nil || !names[r.Name] {
			return nil, errors.New("invalid recorded tool result")
		}
		if _, ok := results[k]; ok {
			return nil, errors.New("duplicate recorded action; results must be deterministic")
		}
		results[k] = r
	}
	return results, nil
}

// Run builds exactly one profile without its backup chain. build MUST use the
// existing authenticated profile factory, not a router/resilience wrapper.
func Run(ctx context.Context, c config.Config, s Spec, build func(config.CloudProfile) (inference.Provider, error)) (Report, error) {
	report := Report{Profile: s.Profile, Model: s.Model, MaxRequestedOutputTokens: 2 * s.MaxRequests * s.MaxTokens}
	results, err := validate(s)
	if err != nil {
		return report, err
	}
	if c.LocusMode == "open_only" || c.LocusMode == "local_only" {
		return report, errors.New("cloud diagnostic prohibited by local-only policy")
	}
	p, ok := c.Profile(s.Profile)
	if !ok || p.Flavor != "chat_completions" {
		return report, errors.New("diagnostic requires an existing chat_completions profile")
	}
	// Validate the pinned endpoint before touching credentials.
	bounds := openai.ReasoningDiagnosticConfig{Mode: openai.ReasoningDrop, BaseURL: p.BaseURL, Model: s.Model, MaxRequests: s.MaxRequests, MaxBodyBytes: 8 << 20, MaxMemoryBytes: 64 << 20, Timeout: time.Duration(s.TimeoutSeconds) * time.Second}
	probe, err := openai.NewReasoningDiagnostic(bounds)
	if err != nil {
		return report, err
	}
	probe.Close()
	ctx, cancel := context.WithTimeout(ctx, bounds.Timeout)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return report, err
	}
	p.Model = s.Model
	provider, err := build(p)
	if err != nil || provider == nil {
		return report, errors.New("diagnostic profile unavailable; check its normal authentication status")
	}
	modes := []openai.ReasoningDiagnosticMode{openai.ReasoningDrop, openai.ReasoningPreserve}
	if s.PreserveFirst {
		modes[0], modes[1] = modes[1], modes[0]
	}
	for _, mode := range modes {
		bounds.Mode = mode
		report.Arms = append(report.Arms, runArm(ctx, provider, s, bounds, results))
	}
	a, b := report.Arms[0].Steps, report.Arms[1].Steps
	for i := 0; i < len(a) && i < len(b); i++ {
		report.ComparableInputs = append(report.ComparableInputs, a[i].NormalizedRequestHash == b[i].NormalizedRequestHash)
	}
	return report, nil
}

func runArm(ctx context.Context, p inference.Provider, s Spec, bounds openai.ReasoningDiagnosticConfig, results map[string]RecordedResult) Arm {
	arm := Arm{Mode: bounds.Mode, Status: "request_budget"}
	session, err := openai.NewReasoningDiagnostic(bounds)
	if err != nil {
		arm.Status = "invalid_session"
		return arm
	}
	defer session.Close()
	ctx = openai.WithReasoningDiagnostic(ctx, session)
	history := append([]llm.Message(nil), s.Messages...)
	seen := map[string]bool{}
	// Bound expansion from a small tool call into a large recorded result BEFORE
	// request JSON encoding. Six bytes per input byte covers JSON escaping.
	seed, _ := json.Marshal(llm.ChatRequest{Model: s.Model, System: s.System, Messages: s.Messages, Tools: s.Tools})
	remaining := MaxInputBytes - len(seed)

	for i := 0; i < s.MaxRequests; i++ {
		if ctx.Err() != nil {
			arm.Status = "cancelled_or_timeout"
			return arm
		}
		stream, err := p.StreamChat(ctx, llm.ChatRequest{Model: s.Model, System: s.System, Messages: history, Tools: s.Tools, Temperature: s.Temperature, MaxTokens: s.MaxTokens})
		if err != nil {
			arm.Status = "provider_or_capture_error"
			if ctx.Err() != nil {
				arm.Status = "cancelled_or_timeout"
			}
			return arm
		}
		response, err := llm.CollectStream(ctx, stream, nil, nil)
		stream.Close()
		if err != nil {
			arm.Status = "stream_error"
			return arm
		}
		captures := session.Captures()
		if len(captures) != i+1 {
			arm.Status = "capture_missing"
			return arm
		}
		cap := captures[i]
		var wire map[string]any
		if json.Unmarshal(cap.Request, &wire) != nil {
			arm.Status = "invalid_capture"
			return arm
		}
		if msgs, ok := wire["messages"].([]any); ok {
			for _, m := range msgs {
				if obj, ok := m.(map[string]any); ok {
					delete(obj, "reasoning_content")
				}
			}
		}
		normalized, _ := json.Marshal(wire)
		step := Step{RequestHash: hash(cap.Request), ResponseHash: hash(cap.Response), NormalizedRequestHash: hash(normalized), ReasoningArrived: cap.ReasoningArrived, ReasoningReplayed: cap.ReasoningReplayed, Finish: cap.FinishReason, Usage: response.Usage, ServedModel: response.Model}
		var toolResults []llm.Block
		var batch []string
		failure := ""
		for _, b := range response.Blocks {
			remaining -= 6*len(b.Text) + 256
			if remaining < 0 {
				failure = "replay_memory_budget"
				break
			}
			if b.Type == llm.BlockToolUse {
				if len(step.ToolIDs) >= 64 || len(b.ToolUseID) > 256 {
					failure = "tool_metadata_limit"
					break
				}

				step.ToolIDs = append(step.ToolIDs, b.ToolUseID)
				k, e := key(b.ToolName, b.ToolInput)
				r, ok := results[k]
				if e != nil || !ok {
					failure = "unrecorded_tool_call"
					continue
				}
				remaining -= 6*(len(b.ToolInput)+len(b.ToolName)+len(b.ToolUseID)+len(r.Content)) + 256
				if remaining < 0 {
					failure = "replay_memory_budget"
					break
				}
				// Include action AND recorded result; exclude newly generated call IDs.
				fingerprint, _ := json.Marshal(r)
				batch = append(batch, string(fingerprint))
				toolResults = append(toolResults, llm.Block{Type: llm.BlockToolResult, ToolUseRef: b.ToolUseID, Content: r.Content, IsError: r.IsError})
			}
		}
		if len(batch) > 0 {
			raw, _ := json.Marshal(batch)
			step.BatchHash = hash(raw)
		}
		arm.Steps = append(arm.Steps, step)
		if failure != "" {
			arm.Status = failure
			return arm
		}
		if len(step.ToolIDs) == 0 {
			if step.Finish == "stop" {
				arm.Status = "terminal_stop"
			} else {
				arm.Status = "nonterminal_finish"
			}
			return arm
		}
		if step.Finish != "tool_calls" {
			arm.Status = "nonterminal_finish"
			return arm
		}
		if seen[step.BatchHash] {
			arm.Status = "repeated_action_result_batch"
			return arm
		}
		seen[step.BatchHash] = true
		history = append(history, llm.Message{Role: llm.RoleAssistant, Blocks: response.Blocks}, llm.Message{Role: llm.RoleUser, Blocks: toolResults})
	}
	return arm
}
