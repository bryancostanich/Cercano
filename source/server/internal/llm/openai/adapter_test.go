package openai

import (
	"testing"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

func TestMessagesToOpenAI_SystemAndText(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
	}, "be terse")
	if len(msgs) != 2 || msgs[0].Role != goopenai.ChatMessageRoleSystem || msgs[0].Content != "be terse" {
		t.Fatalf("system msg wrong: %+v", msgs)
	}
	if msgs[1].Role != goopenai.ChatMessageRoleUser || msgs[1].Content != "hi" {
		t.Fatalf("user msg wrong: %+v", msgs[1])
	}
}

func TestMessagesToOpenAI_ImageURLAndBase64(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "look"},
		{Type: llm.BlockImage, ImageURL: "https://x/y.png"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: "QUJD"},
	}}}, "")
	m := msgs[len(msgs)-1]
	if len(m.MultiContent) != 3 {
		t.Fatalf("want 3 parts, got %d", len(m.MultiContent))
	}
	if m.MultiContent[1].ImageURL.URL != "https://x/y.png" {
		t.Errorf("url part = %q", m.MultiContent[1].ImageURL.URL)
	}
	if m.MultiContent[2].ImageURL.URL != "data:image/png;base64,QUJD" {
		t.Errorf("data-uri part = %q", m.MultiContent[2].ImageURL.URL)
	}
}

func TestMessagesToOpenAI_ToolUseAndResult(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "c1", ToolName: "read", ToolInput: []byte(`{"p":"x"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "c1", Content: "FILE"}}},
	}, "")
	asst := msgs[0]
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "c1" || asst.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("assistant tool_call wrong: %+v", asst)
	}
	tool := msgs[1]
	if tool.Role != goopenai.ChatMessageRoleTool || tool.ToolCallID != "c1" || tool.Content != "FILE" {
		t.Fatalf("tool result wrong: %+v", tool)
	}
}

// Foreign blocks (e.g. reasoning recorded on the Responses backend) have no
// chat-completions representation and are skipped by the block switch. A
// message left with no content and no tool calls must be dropped whole —
// strict compat endpoints reject empty messages, and an empty assistant turn
// is junk context everywhere else.
func TestMessagesToOpenAI_DropsMessagesLeftEmpty(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, ReasoningID: "rs_1", ReasoningData: "gAAAAA"},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "next"}}},
	}, "")
	if len(msgs) != 1 {
		t.Fatalf("expected reasoning-only message dropped, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != goopenai.ChatMessageRoleUser || msgs[0].Content != "next" {
		t.Errorf("surviving message wrong: %+v", msgs[0])
	}
}

// An assistant message whose only payload is tool calls has empty text but is
// NOT empty — it must survive the empty-message drop.
func TestMessagesToOpenAI_KeepsToolCallOnlyMessages(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockToolUse, ToolUseID: "call_1", ToolName: "read_file", ToolInput: []byte(`{}`)},
		}},
	}, "")
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("tool-call-only message must survive: %+v", msgs)
	}
}

// TestBlocksFromOpenAI_SuppressesLeakedToolCallText pins the qwen3/mistral.rs
// leak fix: when an assistant message carries a real structured tool_call and a
// leading text block that is just the tool call written out as a
// [{"name":...,"arguments":...}] array, the text block is dropped. Cases 1 and 2
// use the exact shapes observed in the live conversation store.
func TestBlocksFromOpenAI_SuppressesLeakedToolCallText(t *testing.T) {
	toolCall := goopenai.ToolCall{
		ID: "call-1", Type: goopenai.ToolTypeFunction,
		Function: goopenai.FunctionCall{Name: "Glob", Arguments: `{"pattern":"scratch/*"}`},
	}

	// Case 1: leaked text names the SAME tool as the structured call -> drop text.
	m1 := goopenai.ChatCompletionMessage{
		Content:   `[{"name":"Glob","arguments":{"pattern":"scratch/*"}}]`,
		ToolCalls: []goopenai.ToolCall{toolCall},
	}
	b1 := blocksFromOpenAI(m1)
	if len(b1) != 1 || b1[0].Type != llm.BlockToolUse {
		t.Fatalf("case1: expected only the tool_use block, got %+v", b1)
	}

	// Case 2: leaked text names a DIFFERENT tool than the structured call -> still
	// drop text (guard keys on shape + tool_call presence, not name match).
	m2 := goopenai.ChatCompletionMessage{
		Content:   `[{"name":"Bash","arguments":{"cmd":["ls"]}}]`,
		ToolCalls: []goopenai.ToolCall{toolCall},
	}
	b2 := blocksFromOpenAI(m2)
	if len(b2) != 1 || b2[0].Type != llm.BlockToolUse {
		t.Fatalf("case2: expected only the tool_use block, got %+v", b2)
	}

	// Case 3: genuine prose alongside a tool_call -> keep both.
	m3 := goopenai.ChatCompletionMessage{
		Content:   "Let me search the scratch directory.",
		ToolCalls: []goopenai.ToolCall{toolCall},
	}
	b3 := blocksFromOpenAI(m3)
	if len(b3) != 2 || b3[0].Type != llm.BlockText || b3[1].Type != llm.BlockToolUse {
		t.Fatalf("case3: expected text + tool_use, got %+v", b3)
	}

	// Case 4: tool-call-shaped text with NO structured tool_call sibling -> keep
	// it. Without a real tool_call we cannot safely assume it is a leak, and
	// dropping it would lose the only content of the turn.
	m4 := goopenai.ChatCompletionMessage{
		Content: `[{"name":"Glob","arguments":{"pattern":"scratch/*"}}]`,
	}
	b4 := blocksFromOpenAI(m4)
	if len(b4) != 1 || b4[0].Type != llm.BlockText {
		t.Fatalf("case4: expected the text block preserved, got %+v", b4)
	}

	// Case 5: a JSON array that is not tool-call shaped (no name) rides with a
	// tool_call -> keep the text; it is not a leak.
	m5 := goopenai.ChatCompletionMessage{
		Content:   `[{"result":42}]`,
		ToolCalls: []goopenai.ToolCall{toolCall},
	}
	b5 := blocksFromOpenAI(m5)
	if len(b5) != 2 || b5[0].Type != llm.BlockText {
		t.Fatalf("case5: expected text + tool_use, got %+v", b5)
	}
}

// TestBlocksFromOpenAI_RecoversReasoningWhenContentEmpty covers the GLM-4.5-Air
// failure: llama-server can place the plaintext final answer in reasoning_content
// and leave content empty. We recover it as visible text, but only when there is
// no real content text block and no tool calls.
func TestBlocksFromOpenAI_RecoversReasoningWhenContentEmpty(t *testing.T) {
	// Case A: content empty, reasoning populated, no tool calls -> recover as text.
	mA := goopenai.ChatCompletionMessage{
		Content:          "",
		ReasoningContent: "The answer is 42.",
	}
	bA := blocksFromOpenAI(mA)
	if len(bA) != 1 || bA[0].Type != llm.BlockText || bA[0].Text != "The answer is 42." {
		t.Fatalf("caseA: expected one recovered text block, got %+v", bA)
	}

	// Case B: content present AND reasoning present -> content wins, reasoning ignored.
	mB := goopenai.ChatCompletionMessage{
		Content:          "Real answer.",
		ReasoningContent: "internal thinking",
	}
	bB := blocksFromOpenAI(mB)
	if len(bB) != 1 || bB[0].Type != llm.BlockText || bB[0].Text != "Real answer." {
		t.Fatalf("caseB: expected only the content text block, got %+v", bB)
	}

	// Case C: both empty -> no blocks.
	mC := goopenai.ChatCompletionMessage{}
	bC := blocksFromOpenAI(mC)
	if len(bC) != 0 {
		t.Fatalf("caseC: expected no blocks, got %+v", bC)
	}

	// Case D: empty content + reasoning + a real tool_call -> do NOT promote
	// reasoning; the tool call is the action and reasoning is just thinking.
	toolCall := goopenai.ToolCall{
		ID: "call-1", Type: goopenai.ToolTypeFunction,
		Function: goopenai.FunctionCall{Name: "Glob", Arguments: `{"pattern":"scratch/*"}`},
	}
	mD := goopenai.ChatCompletionMessage{
		Content:          "",
		ReasoningContent: "I should search scratch.",
		ToolCalls:        []goopenai.ToolCall{toolCall},
	}
	bD := blocksFromOpenAI(mD)
	if len(bD) != 1 || bD[0].Type != llm.BlockToolUse {
		t.Fatalf("caseD: expected only the tool_use block, got %+v", bD)
	}
}
