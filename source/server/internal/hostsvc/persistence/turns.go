package persistence

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

const contextTurnPreviewMax = 120
const contextTurnBodyMax = 4096

// ContextTurnView derives a display summary (kind, preview, body, token estimate)
// from a stored turn. Pure — no I/O. Multi-block turns (e.g. an assistant turn
// with interleaved text and tool_use) contribute every block in order to the
// preview and body; earlier implementations overwrote preview/body on each tool
// block, which silently dropped prose from mixed turns and made /c look like
// there was almost no conversation. Kind still reflects the last non-text
// block's kind (matching what the client expects for styling).
func ContextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn {
	kind := "text"
	tokenSrc := t.Content
	var previewParts, bodyParts []string
	// Tool metadata from the turn's first tool block — carried so the /c
	// viewer can reuse the main chat's rich tool renderers (highlight, diff).
	var toolName, toolUseID, toolArgs, toolUseRef string
	var toolStartLine int

	if t.BlocksJSON != "" {
		var blocks []llm.Block
		if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err == nil {
			tokenSrc = t.BlocksJSON
			for _, b := range blocks {
				switch b.Type {
				case llm.BlockToolUse:
					kind = "tool_use"
					previewParts = append(previewParts, b.ToolName+"("+ctPreview(string(b.ToolInput))+")")
					bodyParts = append(bodyParts, b.ToolName+" "+string(b.ToolInput))
					if toolName == "" {
						toolName = b.ToolName
						toolUseID = b.ToolUseID
						toolArgs, _ = capBody(string(b.ToolInput), contextTurnBodyMax)
					}
				case llm.BlockToolResult:
					kind = "tool_result"
					previewParts = append(previewParts, "→ "+ctPreview(b.Content))
					bodyParts = append(bodyParts, b.Content)
					if toolUseRef == "" {
						toolUseRef = b.ToolUseRef
						toolStartLine = b.StartLine
					}
				case llm.BlockText:
					if b.Text == "" {
						continue
					}
					previewParts = append(previewParts, b.Text)
					bodyParts = append(bodyParts, b.Text)
				case llm.BlockImage:
					kind = "image"
					previewParts = append(previewParts, "[image]")
					bodyParts = append(bodyParts, "[image]")
				}
			}
		} else if t.Content != "" {
			// Malformed BlocksJSON — fall back to raw content so the turn
			// isn't invisible in /c.
			previewParts = append(previewParts, t.Content)
			bodyParts = append(bodyParts, t.Content)
		}
	} else if t.Content != "" {
		previewParts = append(previewParts, t.Content)
		bodyParts = append(bodyParts, t.Content)
	}

	preview := strings.Join(previewParts, " ⏵ ")
	body := strings.Join(bodyParts, "\n\n")

	bodyStr, truncated := capBody(body, contextTurnBodyMax)
	return &proto.ContextTurn{
		Id:            t.ID,
		Role:          t.Role,
		Kind:          kind,
		Preview:       ctTruncate(ctPreview(preview), contextTurnPreviewMax),
		Body:          bodyStr,
		Truncated:     truncated,
		EstTokens:     int32(tok.Count(tokenSrc)),
		ToolName:      toolName,
		ToolUseId:     toolUseID,
		ToolArgs:      toolArgs,
		ToolUseRef:    toolUseRef,
		ToolStartLine: int32(toolStartLine),
	}
}

// capBody truncates s to <= max bytes on a rune boundary, reporting whether it cut.
func capBody(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max], true
}

func ctPreview(s string) string { return strings.Join(strings.Fields(s), " ") }

// CtTruncate truncates s to <= max bytes on a rune boundary, appending "…".
// Exported so package server's test shims can reference it without code duplication.
func CtTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + "…"
}

// ctTruncate is the unexported alias used within this package.
func ctTruncate(s string, max int) string { return CtTruncate(s, max) }
