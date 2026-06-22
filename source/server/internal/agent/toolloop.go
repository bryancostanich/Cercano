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
		allErrored := true
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
			_ = mode
			_ = tier
			res, ierr := tool.Execute(ctx, tc.ToolInput)
			if ierr != nil {
				results = append(results, llm.Block{
					Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
					Content: ierr.Error(), IsError: true,
				})
				continue
			}
			allErrored = false
			results = append(results, llm.Block{
				Type: llm.BlockToolResult, ToolUseRef: tc.ToolUseID,
				Content: res.Text, IsError: false,
			})
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
