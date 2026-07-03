package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// transcriptLine is the subset of a Claude Code JSONL record we use.
type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock covers the block shapes inside message.content / tool_result.content.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// LoadTranscript reads a Claude Code JSONL session file and returns it as a
// pairing-valid []llm.Message, sliced to <= maxTokens from the start.
func LoadTranscript(path string, maxTokens int, tok contextmeter.Tokenizer) ([]llm.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseTranscript(f, maxTokens, tok), nil
}

// parseTranscript converts the JSONL stream to messages. user/assistant lines
// become messages; thinking blocks and system/metadata lines are skipped.
// The result is truncated to <= maxTokens (from the start) and pairing-repaired.
func parseTranscript(r io.Reader, maxTokens int, tok contextmeter.Tokenizer) []llm.Message {
	var msgs []llm.Message
	used := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024) // transcript lines can be large
	for sc.Scan() {
		var tl transcriptLine
		if json.Unmarshal(sc.Bytes(), &tl) != nil {
			continue
		}
		var role llm.Role
		switch tl.Type {
		case "user":
			role = llm.RoleUser
		case "assistant":
			role = llm.RoleAssistant
		default:
			continue
		}
		blocks := parseContent(tl.Message.Content)
		if len(blocks) == 0 {
			continue
		}
		m := llm.Message{Role: role, Blocks: blocks}
		mt := compaction.MessageTokens(tok, m)
		if maxTokens > 0 && len(msgs) > 0 && used+mt > maxTokens {
			break
		}
		msgs = append(msgs, m)
		used += mt
	}
	return llm.RepairPairing(msgs)
}

// parseContent handles message.content as a plain string or an array of blocks.
func parseContent(raw json.RawMessage) []llm.Block {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []llm.Block{{Type: llm.BlockText, Text: s}}
	}
	var cbs []contentBlock
	if json.Unmarshal(raw, &cbs) != nil {
		return nil
	}
	var out []llm.Block
	for _, b := range cbs {
		switch b.Type {
		case "text":
			if b.Text != "" {
				out = append(out, llm.Block{Type: llm.BlockText, Text: b.Text})
			}
		case "tool_use":
			out = append(out, llm.Block{
				Type: llm.BlockToolUse, ToolUseID: b.ID, ToolName: b.Name, ToolInput: b.Input,
			})
		case "tool_result":
			out = append(out, llm.Block{
				Type: llm.BlockToolResult, ToolUseRef: b.ToolUseID, Content: stringifyResult(b.Content),
			})
		}
		// "thinking" and any other block types are skipped.
	}
	return out
}

// stringifyResult flattens a tool_result content (string, or array of text
// blocks) into plain text.
func stringifyResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var cbs []contentBlock
	if json.Unmarshal(raw, &cbs) == nil {
		var b strings.Builder
		for _, c := range cbs {
			if c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
		return b.String()
	}
	return ""
}
