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
		"auto_exit",
		"restart_agent",
		"reasoning_diagnostic":
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
// backing file (the disk re-reads no-op on the empty path). Used for
// pre-authorized sub-agent loops, where a human already approved the granted
// toolset via the dispatch call's own confirm — never persist one of these.
//
// The MCP allowlist is empty: with no backing file there is nothing to read.
// Callers that gate MCP tools (the worker) MUST use
// NewStaticPermissionStoreWithMCPAllow and pass the host's patterns, or every
// allowlisted tool silently re-prompts.
func NewStaticPermissionStore(m PermissionMode) *PermissionStore {
	return &PermissionStore{mode: m}
}

// NewStaticPermissionStoreWithMCPAllow returns a file-less store pinned to mode
// and carrying an explicit MCP allowlist. The worker uses this: it has no
// access to the host's permissions.yaml, so the host ships both the mode and
// the allowlist patterns in StartTurn and the worker's gating then matches the
// host's exactly. An empty allow slice means "nothing allowlisted" — the same
// answer the host gives for an empty file.
func NewStaticPermissionStoreWithMCPAllow(m PermissionMode, allow []string) *PermissionStore {
	return &PermissionStore{mode: m, mcpAllow: append([]string(nil), allow...)}
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
// ApplyRuntimeUpdate replaces the mode and MCP allowlist of a file-less store.
// The in-process tool loop re-reads permissions.yaml per gate decision so a
// mid-turn tightening takes effect immediately; a worker has no file to re-read,
// so the host pushes changes over the turn stream and the worker applies them
// here. Without this, a worker turn would keep gating on the values captured at
// StartTurn — strictly more permissive than in-process for the rest of the turn.
//
// NOTE: no host-side caller sends that push yet, so this is currently exercised
// only by tests and the gap above is live. See
// docs/bugs/2026-09-17-worker-mcp-permission-liveness.md.
//
// File-backed stores ignore this: their own re-read is the source of truth, and
// letting a push overwrite it would race with the watcher.
func (s *PermissionStore) ApplyRuntimeUpdate(m PermissionMode, mcpAllow []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		return
	}
	s.mode = m
	s.mcpAllow = append([]string(nil), mcpAllow...)
}

// MCPAllowPatterns returns a copy of the current MCP allowlist patterns,
// re-reading the backing file first so the answer reflects live edits. The
// worker runner ships these to the worker in StartTurn: gating there must match
// the host's, and the worker cannot read permissions.yaml itself.
func (s *PermissionStore) MCPAllowPatterns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if data, err := os.ReadFile(s.path); err == nil {
			var f permsFile
			if yaml.Unmarshal(data, &f) == nil {
				s.mcpAllow = f.MCPAllow
			}
		}
	}
	return append([]string(nil), s.mcpAllow...)
}

func (s *PermissionStore) IsMCPAllowed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// File-backed stores re-read so a live permissions.yaml edit takes effect
	// without a restart. File-less stores (the worker's) keep the allowlist they
	// were constructed with: reading the empty path always fails, which would
	// otherwise clobber nothing but also never populate, making every
	// allowlisted tool re-prompt.
	if s.path != "" {
		if data, err := os.ReadFile(s.path); err == nil {
			var f permsFile
			if yaml.Unmarshal(data, &f) == nil {
				s.mcpAllow = f.MCPAllow
			}
		}
	}
	for _, pat := range s.mcpAllow {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}
