package agent

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

type PermissionMode string

const (
	ModeStrict     PermissionMode = "strict"
	ModePermissive PermissionMode = "permissive"
	ModeBypass     PermissionMode = "bypass"
)

func ParseMode(s string) (PermissionMode, error) {
	switch PermissionMode(s) {
	case ModeStrict, ModePermissive, ModeBypass:
		return PermissionMode(s), nil
	}
	return "", fmt.Errorf("unknown permission mode: %q (want strict|permissive|bypass)", s)
}

// GateDecision returns true when a tool call at the given tier requires
// human confirmation under the given mode.
func GateDecision(mode PermissionMode, tier llm.Permission) bool {
	if tier == llm.PermR {
		return false
	}
	switch mode {
	case ModeStrict:
		return true
	case ModePermissive:
		return tier == llm.PermX
	case ModeBypass:
		return false
	}
	return true
}
