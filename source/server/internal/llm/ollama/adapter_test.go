package ollama

import (
	"encoding/json"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestBlockToOllama_Text(t *testing.T) {
	msg := messageToOllama(llm.Message{
		Role:   llm.RoleUser,
		Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}},
	})
	if msg.Role != "user" || msg.Content != "hi" {
		t.Errorf("ollama msg: %+v", msg)
	}
}

func TestBlockToOllama_ToolUse_InAssistant(t *testing.T) {
	msg := messageToOllama(llm.Message{
		Role: llm.RoleAssistant,
		Blocks: []llm.Block{{
			Type: llm.BlockToolUse, ToolUseID: "u1",
			ToolName: "read_file", ToolInput: json.RawMessage(`{"path":"x"}`),
		}},
	})
	if msg.Role != "assistant" || len(msg.ToolCalls) != 1 {
		t.Errorf("expected one tool_call, got %+v", msg)
	}
	if msg.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("function name: %+v", msg.ToolCalls[0])
	}
	if v, ok := msg.ToolCalls[0].Function.Arguments.Get("path"); !ok || v != "x" {
		t.Errorf("arguments: %+v", msg.ToolCalls[0].Function.Arguments)
	}
}

func TestBlockToOllama_ToolResult_InUser(t *testing.T) {
	msg := messageToOllama(llm.Message{
		Role: llm.RoleUser,
		Blocks: []llm.Block{{
			Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "32 lines",
		}},
	})
	if msg.Role != "tool" || msg.Content != "32 lines" {
		t.Errorf("tool result in user msg: %+v", msg)
	}
}

func TestToolsToOllama_PanicsOnBadSchema(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on bad schema")
		}
	}()
	_ = toolsToOllama([]llm.Tool{{Name: "x", Schema: json.RawMessage(`{not json`)}})
}

func TestToolsToOllama_PassesPropertiesAndRequired(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"file path"}},"required":["path"]}`)
	out := toolsToOllama([]llm.Tool{{Name: "read_file", Description: "reads a file", Schema: schema}})
	if len(out) != 1 {
		t.Fatalf("expected one tool, got %d", len(out))
	}
	if out[0].Function.Name != "read_file" {
		t.Errorf("name: %s", out[0].Function.Name)
	}
	if out[0].Function.Parameters.Type != "object" {
		t.Errorf("parameters.type: %s", out[0].Function.Parameters.Type)
	}
	if len(out[0].Function.Parameters.Required) != 1 || out[0].Function.Parameters.Required[0] != "path" {
		t.Errorf("required: %+v", out[0].Function.Parameters.Required)
	}
	if out[0].Function.Parameters.Properties == nil {
		t.Fatalf("properties nil")
	}
	prop, ok := out[0].Function.Parameters.Properties.Get("path")
	if !ok {
		t.Fatalf("missing path property")
	}
	if len(prop.Type) != 1 || prop.Type[0] != "string" {
		t.Errorf("prop type: %+v", prop.Type)
	}
}
