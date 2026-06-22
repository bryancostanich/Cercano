package llm

import "encoding/json"

type Permission string

const (
	PermR Permission = "R"
	PermW Permission = "W"
	PermX Permission = "X"
)

type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Permission  Permission
}

type ToolChoiceType string

const (
	ToolChoiceAuto ToolChoiceType = "auto"
	ToolChoiceAny  ToolChoiceType = "any"
	ToolChoiceTool ToolChoiceType = "tool"
	ToolChoiceNone ToolChoiceType = "none"
)

type ToolChoice struct {
	Type ToolChoiceType
	Name string
}
