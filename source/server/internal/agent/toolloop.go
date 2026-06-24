package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

type LoopEventKind string

const (
	LoopToolUseStart       LoopEventKind = "tool_use_start"
	LoopToolUseStop        LoopEventKind = "tool_use_stop"
	LoopToolExecStart      LoopEventKind = "tool_exec_start"
	LoopToolExecComplete   LoopEventKind = "tool_exec_complete"
	LoopPermissionRequired LoopEventKind = "permission_required"
)

type LoopEvent struct {
	Kind      LoopEventKind
	ToolUseID string
	ToolName  string
	ArgsJSON  string
	Tier      string
	Summary   string
	Detail    string
	IsError   bool
}

type ToolLoopInput struct {
	Provider    llm.Provider
	Registry    *agenttools.Registry
	Permissions *PermissionStore
	ConvHistory []llm.Message
	UserInput   string
	Model       string
	System      string

	// PermissionRequester is the callback the loop uses to surface a
	// confirm prompt to the active client (nil = auto-allow, useful in tests).
	PermissionRequester func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission) (allow bool, err error)

	// EventSink receives lifecycle events as the loop runs. Nil-safe.
	EventSink func(ev LoopEvent)

	// OnTextDelta, when set, receives assistant text deltas as they stream from
	// the provider, so the server can forward them to the client for live
	// token-by-token rendering. Nil-safe.
	OnTextDelta func(string)
}

type ToolLoopResult struct {
	FinalText    string
	FinalBlocks  []llm.Block
	Iterations   int
	History      []llm.Message
	InputTokens  int // last LLM call's provider-reported input tokens (context occupancy)
	OutputTokens int // last LLM call's provider-reported output tokens
}

const MaxToolLoopIterations = 10

func summarizeResult(res *agenttools.Result) string {
	if res == nil {
		return ""
	}
	// Prefer the curated one-line Note (e.g. Bash's "exit 1 · 2ms", or a
	// truncation/fallback caveat). It's purpose-built for a glance; the raw
	// result text is not.
	if res.Note != "" {
		return truncateRunes(res.Note, 80)
	}
	switch res.Type {
	case agenttools.ResultText:
		// First line only, truncated rune-safely (the old text[:80] sliced
		// bytes and could split a multibyte rune into invalid UTF-8).
		return truncateRunes(firstLine(res.Text), 80)
	case agenttools.ResultRows:
		return fmt.Sprintf("rows: %d", len(res.Rows))
	case agenttools.ResultJSON:
		return fmt.Sprintf("json: %d bytes", len(res.JSON))
	}
	return ""
}

// firstLine returns s up to the first newline.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// truncateRunes caps s to max runes, appending an ellipsis when it cuts. It
// counts runes (not bytes), so it never splits a multibyte character.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func RunToolLoop(ctx context.Context, in ToolLoopInput) (ToolLoopResult, error) {
	if !in.Provider.Capabilities().SupportsTools {
		return ToolLoopResult{}, fmt.Errorf("provider %s does not support tools", in.Provider.Name())
	}

	emit := func(ev LoopEvent) {
		if in.EventSink != nil {
			in.EventSink(ev)
		}
	}

	hist := append([]llm.Message{}, in.ConvHistory...)
	hist = append(hist, llm.Message{
		Role:   llm.RoleUser,
		Blocks: []llm.Block{{Type: llm.BlockText, Text: in.UserInput}},
	})

	catalog := agenttools.BuildToolCatalog(in.Registry)
	mode := ModePermissive
	if in.Permissions != nil {
		mode = in.Permissions.Mode()
	}
	consecutiveErrors := 0
	var lastIn, lastOut int

	for iter := 0; iter < MaxToolLoopIterations; iter++ {
		req := llm.ChatRequest{
			Model:     in.Model,
			System:    in.System,
			Messages:  hist,
			Tools:     catalog,
			MaxTokens: 4096,
		}
		rdr, err := in.Provider.StreamChat(ctx, req)
		if err != nil {
			return ToolLoopResult{}, err
		}
		resp, err := collectStream(ctx, rdr, in.OnTextDelta)
		rdr.Close()
		if err != nil {
			return ToolLoopResult{}, err
		}
		lastIn, lastOut = resp.InputTokens, resp.OutputTokens
		hist = append(hist, llm.Message{Role: llm.RoleAssistant, Blocks: resp.Blocks})

		var toolCalls []llm.Block
		var finalText string
		for _, b := range resp.Blocks {
			if b.Type == llm.BlockToolUse {
				toolCalls = append(toolCalls, b)
			}
			if b.Type == llm.BlockText {
				finalText += b.Text
			}
		}
		if len(toolCalls) == 0 {
			return ToolLoopResult{
				FinalText: finalText, FinalBlocks: resp.Blocks,
				Iterations: iter + 1, History: hist,
				InputTokens: lastIn, OutputTokens: lastOut,
			}, nil
		}

		for _, tc := range toolCalls {
			emit(LoopEvent{Kind: LoopToolUseStart, ToolUseID: tc.ToolUseID, ToolName: tc.ToolName})
			emit(LoopEvent{Kind: LoopToolUseStop, ToolUseID: tc.ToolUseID, ToolName: tc.ToolName, ArgsJSON: string(tc.ToolInput)})
		}

		results := make([]llm.Block, 0, len(toolCalls))

		type pendingCall struct {
			block llm.Block
			tool  agenttools.Tool
			tier  llm.Permission
		}
		var rCalls, wxCalls []pendingCall
		for _, tc := range toolCalls {
			tool, ok := in.Registry.Get(tc.ToolName)
			if !ok {
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: "unknown tool: " + tc.ToolName, IsError: true,
				})
				continue
			}
			tier := agenttools.PermissionToLLM(tool.Permission())
			pc := pendingCall{block: tc, tool: tool, tier: tier}
			if tier == llm.PermR {
				rCalls = append(rCalls, pc)
			} else {
				wxCalls = append(wxCalls, pc)
			}
		}

		type rr struct {
			idx int
			res llm.Block
		}
		rChan := make(chan rr, len(rCalls))
		for i, pc := range rCalls {
			go func(i int, pc pendingCall) {
				emit(LoopEvent{Kind: LoopToolExecStart, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName})
				res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
				out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
				if err != nil {
					out.Content = err.Error()
					out.IsError = true
					emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: err.Error(), IsError: true})
				} else {
					out.Content = res.LLMContent()
					emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: summarizeResult(res), Detail: res.Detail, IsError: false})
				}
				rChan <- rr{idx: i, res: out}
			}(i, pc)
		}
		rResults := make([]llm.Block, len(rCalls))
		for range rCalls {
			r := <-rChan
			rResults[r.idx] = r.res
		}
		results = append(results, rResults...)

		for _, pc := range wxCalls {
			if GateDecision(mode, pc.tier) {
				if in.PermissionRequester == nil {
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "no permission requester wired", IsError: true,
					})
					hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})
					return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist, InputTokens: lastIn, OutputTokens: lastOut}, nil
				}
				emit(LoopEvent{Kind: LoopPermissionRequired, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, ArgsJSON: string(pc.block.ToolInput), Tier: string(pc.tier)})
				allow, err := in.PermissionRequester(ctx, pc.block.ToolUseID, pc.block.ToolName, pc.block.ToolInput, pc.tier)
				if err != nil {
					return ToolLoopResult{}, err
				}
				if !allow {
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "user denied execution", IsError: true,
					})
					hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})
					return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist, InputTokens: lastIn, OutputTokens: lastOut}, nil
				}
			}
			emit(LoopEvent{Kind: LoopToolExecStart, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName})
			res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
			out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
			if err != nil {
				out.Content = err.Error()
				out.IsError = true
				emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: err.Error(), IsError: true})
			} else {
				out.Content = res.LLMContent()
				emit(LoopEvent{Kind: LoopToolExecComplete, ToolUseID: pc.block.ToolUseID, ToolName: pc.block.ToolName, Summary: summarizeResult(res), Detail: res.Detail, IsError: false})
			}
			results = append(results, out)
		}

		allErrored := true
		for _, r := range results {
			if !r.IsError {
				allErrored = false
				break
			}
		}
		hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})

		if allErrored {
			consecutiveErrors++
			if consecutiveErrors >= 3 {
				return ToolLoopResult{
					FinalText: finalText, Iterations: iter + 1, History: hist,
					InputTokens: lastIn, OutputTokens: lastOut,
				}, fmt.Errorf("aborted: 3 consecutive iterations of tool errors")
			}
		} else {
			consecutiveErrors = 0
		}
	}
	return ToolLoopResult{Iterations: MaxToolLoopIterations, History: hist, InputTokens: lastIn, OutputTokens: lastOut},
		fmt.Errorf("hit max tool loop iterations (%d)", MaxToolLoopIterations)
}

// collectStream consumes a StreamReader and rebuilds the equivalent
// non-streaming ChatResponse shape the loop logic expects. Text deltas
// concatenate into BlockText; tool_use_input_delta events concatenate
// partial JSON into BlockToolUse.ToolInput.
// collectStream consumes a StreamReader into a ChatResponse. onText, when
// non-nil, is called with each text delta as it arrives so callers can stream
// assistant prose to the client live (the loop otherwise only surfaces the
// fully-buffered text at the end).
func collectStream(ctx context.Context, rdr llm.StreamReader, onText func(string)) (llm.ChatResponse, error) {
	var (
		out         llm.ChatResponse
		currentText strings.Builder
		currentTool *llm.Block
		toolArgsBuf strings.Builder
	)
	flushText := func() {
		if currentText.Len() > 0 {
			out.Blocks = append(out.Blocks, llm.Block{
				Type: llm.BlockText, Text: currentText.String(),
			})
			currentText.Reset()
		}
	}
	flushTool := func() {
		if currentTool != nil {
			if toolArgsBuf.Len() > 0 {
				currentTool.ToolInput = json.RawMessage(toolArgsBuf.String())
			} else if currentTool.ToolInput == nil {
				currentTool.ToolInput = json.RawMessage("{}")
			}
			out.Blocks = append(out.Blocks, *currentTool)
			currentTool = nil
			toolArgsBuf.Reset()
		}
	}
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventTextDelta:
			if currentTool != nil {
				flushTool()
			}
			currentText.WriteString(ev.TextDelta)
			if onText != nil {
				onText(ev.TextDelta)
			}
		case llm.EventToolUseStart:
			flushText()
			flushTool()
			currentTool = &llm.Block{
				Type:      llm.BlockToolUse,
				ToolUseID: ev.ToolUseID,
				ToolName:  ev.ToolName,
			}
		case llm.EventToolUseInputDelta:
			toolArgsBuf.WriteString(ev.TextDelta)
		case llm.EventToolUseStop:
			flushTool()
		case llm.EventMessageStart:
			out.InputTokens = ev.InputTokens
		case llm.EventMessageStop:
			flushText()
			flushTool()
			if ev.StopReason != "" {
				out.StopReason = ev.StopReason
			}
			if ev.OutputTokens > 0 {
				out.OutputTokens = ev.OutputTokens
			}
		case llm.EventError:
			return out, fmt.Errorf("stream error: %s", ev.ErrText)
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
	flushText()
	flushTool()
	return out, nil
}
