package agent

import (
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
)

// RequestBudgetInput describes the provider-facing request shape the tool loop
// is about to send. The estimator is deliberately cheap and conservative; real
// provider usage remains authoritative after a successful call.
type RequestBudgetInput struct {
	System         string
	Messages       []llm.Message
	Tools          []llm.Tool
	MaxTokens      int
	ContextWindow  int
	SafetyFraction float64
}

// RequestBudgetResult is the decomposed prompt-size estimate used for logs,
// tests, and preflight overflow errors.
type RequestBudgetResult struct {
	SystemTokens    int
	MessageTokens   int
	ToolTokens      int
	OutputReserve   int
	FixedTokens     int
	EstimatedUsed   int
	Limit           int
	PromptBudget    int
	Fits            bool
	TrimmedMessages int
}

func (r RequestBudgetResult) OverflowError() error {
	return &llm.Error{
		Class:    llm.ErrContextOverflow,
		Provider: "preflight",
		Used:     r.EstimatedUsed,
		Limit:    r.Limit,
		Err: fmt.Errorf(
			"request is ~%d tokens including ~%d tool tokens and %d reserved output tokens, but this model holds %d; trim the task/history, reduce tools, or raise the model's context window",
			r.EstimatedUsed, r.ToolTokens, r.OutputReserve, r.Limit),
	}
}

func estimateToolTokens(tools []llm.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	b, err := json.Marshal(tools)
	if err != nil {
		// Fall back to counting the visible strings if an unexpected RawMessage is
		// not marshalable. Tool schemas are expected to be valid JSON.
		total := 0
		for _, tool := range tools {
			total += estimateTokens(tool.Name) + estimateTokens(tool.Description) + estimateTokens(string(tool.Schema))
		}
		return total
	}
	return estimateTokens(string(b))
}

func EstimateRequestBudget(in RequestBudgetInput) RequestBudgetResult {
	fraction := in.SafetyFraction
	if fraction <= 0 || fraction > 1 {
		fraction = preflightSafetyFraction
	}
	result := RequestBudgetResult{
		SystemTokens:  estimateTokens(in.System),
		ToolTokens:    estimateToolTokens(in.Tools),
		OutputReserve: in.MaxTokens,
		Limit:         in.ContextWindow,
	}
	for _, m := range in.Messages {
		result.MessageTokens += estimateMessageTokens(m)
	}
	result.FixedTokens = result.SystemTokens + result.ToolTokens + result.OutputReserve
	result.EstimatedUsed = result.FixedTokens + result.MessageTokens
	if in.ContextWindow <= 0 {
		result.PromptBudget = 0
		result.Fits = true
		return result
	}
	result.PromptBudget = int(float64(in.ContextWindow) * fraction)
	result.Fits = result.EstimatedUsed <= result.PromptBudget
	return result
}

// TrimMessagesToBudget drops the oldest messages until the estimated request
// fits. preserveTail is the number of newest messages that must not be dropped
// (normally the current user turn). Pairing is repaired after each candidate so
// provider-native tool_use/tool_result constraints remain valid.
func TrimMessagesToBudget(in RequestBudgetInput, preserveTail int) ([]llm.Message, RequestBudgetResult) {
	messages := append([]llm.Message(nil), in.Messages...)
	if preserveTail < 0 {
		preserveTail = 0
	}
	if preserveTail > len(messages) {
		preserveTail = len(messages)
	}
	result := EstimateRequestBudget(RequestBudgetInput{
		System: in.System, Messages: messages, Tools: in.Tools, MaxTokens: in.MaxTokens,
		ContextWindow: in.ContextWindow, SafetyFraction: in.SafetyFraction,
	})
	if result.Fits || in.ContextWindow <= 0 {
		return messages, result
	}
	for len(messages) > preserveTail {
		messages = append([]llm.Message(nil), messages[1:]...)
		messages = llm.RepairPairing(messages)
		if preserveTail > len(messages) {
			preserveTail = len(messages)
		}
		result = EstimateRequestBudget(RequestBudgetInput{
			System: in.System, Messages: messages, Tools: in.Tools, MaxTokens: in.MaxTokens,
			ContextWindow: in.ContextWindow, SafetyFraction: in.SafetyFraction,
		})
		result.TrimmedMessages = len(in.Messages) - len(messages)
		if result.Fits {
			return messages, result
		}
	}
	result.TrimmedMessages = len(in.Messages) - len(messages)
	return messages, result
}
