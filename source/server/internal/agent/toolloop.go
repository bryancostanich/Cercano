package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

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
	PermissionRequester func(ctx context.Context, name string, args json.RawMessage, tier llm.Permission) (allow bool, err error)
}

type ToolLoopResult struct {
	FinalText   string
	FinalBlocks []llm.Block
	Iterations  int
	History     []llm.Message
}

const MaxToolLoopIterations = 10

func RunToolLoop(ctx context.Context, in ToolLoopInput) (ToolLoopResult, error) {
	if !in.Provider.Capabilities().SupportsTools {
		return ToolLoopResult{}, fmt.Errorf("provider %s does not support tools", in.Provider.Name())
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

	for iter := 0; iter < MaxToolLoopIterations; iter++ {
		req := llm.ChatRequest{
			Model:     in.Model,
			System:    in.System,
			Messages:  hist,
			Tools:     catalog,
			MaxTokens: 4096,
		}
		resp, err := in.Provider.Chat(ctx, req)
		if err != nil {
			return ToolLoopResult{}, err
		}
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
			}, nil
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
				res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
				out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
				if err != nil {
					out.Content = err.Error()
					out.IsError = true
				} else {
					out.Content = res.Text
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
					return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist}, nil
				}
				allow, err := in.PermissionRequester(ctx, pc.block.ToolName, pc.block.ToolInput, pc.tier)
				if err != nil {
					return ToolLoopResult{}, err
				}
				if !allow {
					results = append(results, llm.Block{
						Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID,
						Content: "user denied execution", IsError: true,
					})
					hist = append(hist, llm.Message{Role: llm.RoleUser, Blocks: results})
					return ToolLoopResult{FinalText: finalText, Iterations: iter + 1, History: hist}, nil
				}
			}
			res, err := pc.tool.Execute(ctx, pc.block.ToolInput)
			out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: pc.block.ToolUseID}
			if err != nil {
				out.Content = err.Error()
				out.IsError = true
			} else {
				out.Content = res.Text
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
				}, fmt.Errorf("aborted: 3 consecutive iterations of tool errors")
			}
		} else {
			consecutiveErrors = 0
		}
	}
	return ToolLoopResult{Iterations: MaxToolLoopIterations, History: hist},
		fmt.Errorf("hit max tool loop iterations (%d)", MaxToolLoopIterations)
}
