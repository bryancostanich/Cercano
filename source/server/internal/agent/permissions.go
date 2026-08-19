package agent

import (
	"fmt"
	"os"
	"path"
	"sync"

	"gopkg.in/yaml.v3"

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

// GateDecision returns true when a tool call at the given tier requires human
// confirmation under the given mode. Bypass suppresses permission prompts for
// ordinary tools at every tier; tool-specific human-handoff prompts are layered
// in GateDecisionForTool.
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

// GateDecisionForTool extends GateDecision with tool-specific policy. Bypass
// skips destructive/executing tool prompts, including built-in X-tier tools and
// delegated-agent grant prompts. It still preserves explicit human-handoff
// prompts: those tools are not asking for permission to mutate the workspace,
// they are asking the user to change the conversation's operating mode.
func GateDecisionForTool(mode PermissionMode, tier llm.Permission, toolName string, isMCP, allowlisted bool) bool {
	if mode == ModeBypass && isHumanHandoffTool(toolName) {
		return true
	}
	return GateDecisionForMCP(mode, tier, isMCP, allowlisted)
}

func isHumanHandoffTool(toolName string) bool {
	switch toolName {
	case "suggest_plan",
		"request_plan_approval",
		"suggest_autonomous",
		"request_autonomous_execution",
		"request_autonomous_exit",
		"complete_autonomous_review",
		"auto_exit",
		"restart_agent":
		return true
	default:
		return false
	}
}

// GateDecisionForMCP extends GateDecision with MCP origin. MCP tools are
// untrusted third-party code: they confirm by default even in permissive mode,
// unless allowlisted. Bypass suppresses MCP prompts at every tier.
func GateDecisionForMCP(mode PermissionMode, tier llm.Permission, isMCP, allowlisted bool) bool {
	if mode == ModeBypass {
		return false
	}
	if isMCP {
		return tier == llm.PermX || !allowlisted
	}
	return GateDecision(mode, tier)
}

type PermissionStore struct {
	mu       sync.Mutex
	path     string
	mode     PermissionMode
	mcpAllow []string
}

type permsFile struct {
	Mode     string   `yaml:"mode"`
	MCPAllow []string `yaml:"mcp_allow"`
}

// NewStaticPermissionStore returns an in-memory store pinned to mode, with no
// backing file (Mode()'s disk re-read no-ops on the empty path). Used for
// pre-authorized sub-agent loops, where a human already approved the granted
// toolset via the dispatch call's own confirm — never persist one of these.
func NewStaticPermissionStore(m PermissionMode) *PermissionStore {
	return &PermissionStore{mode: m}
}

func LoadPermissionStore(filePath string) (*PermissionStore, error) {
	s := &PermissionStore{path: filePath, mode: ModePermissive}
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f permsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Mode != "" {
		m, err := ParseMode(f.Mode)
		if err == nil {
			s.mode = m
		}
	}
	s.mcpAllow = f.MCPAllow
	return s, nil
}

// Mode returns the active permission mode, re-reading the file on disk so an
// external edit — a hand-edit, or a SetMode from another client sharing this
// singleton agent — propagates live without a restart. The file is the source
// of truth; the in-memory field is a fallback for when it is transiently
// missing or malformed (in which case the gate must NOT silently flip open, so
// the last-known mode is retained). Mode is consulted per tool-gate decision
// (human-speed), so re-reading a one-line file here is negligible.
func (s *PermissionStore) Mode() PermissionMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, err := os.ReadFile(s.path); err == nil {
		var f permsFile
		if yaml.Unmarshal(data, &f) == nil && f.Mode != "" {
			if m, perr := ParseMode(f.Mode); perr == nil {
				s.mode = m
			}
		}
	}
	return s.mode
}

// persistLocked writes the current in-memory mode and mcpAllow to disk.
// Caller must hold s.mu.
func (s *PermissionStore) persistLocked() error {
	data, err := yaml.Marshal(permsFile{Mode: string(s.mode), MCPAllow: s.mcpAllow})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *PermissionStore) SetMode(m PermissionMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	return s.persistLocked()
}

// AddMCPAllow appends a glob pattern to the MCP allowlist and persists it.
// If the pattern is already present the call is a no-op.
func (s *PermissionStore) AddMCPAllow(pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.mcpAllow {
		if p == pattern {
			return nil
		}
	}
	s.mcpAllow = append(s.mcpAllow, pattern)
	return s.persistLocked()
}

// IsMCPAllowed reports whether an mcp__server__tool name matches any allowlist
// pattern. Re-reads the file so hand-edits take effect live, mirroring Mode().
func (s *PermissionStore) IsMCPAllowed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, err := os.ReadFile(s.path); err == nil {
		var f permsFile
		if yaml.Unmarshal(data, &f) == nil {
			s.mcpAllow = f.MCPAllow
		}
	}
	for _, pat := range s.mcpAllow {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}
