package agent

import (
	"fmt"
	"os"
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

type PermissionStore struct {
	mu   sync.Mutex
	path string
	mode PermissionMode
}

type permsFile struct {
	Mode string `yaml:"mode"`
}

func LoadPermissionStore(path string) (*PermissionStore, error) {
	s := &PermissionStore{path: path, mode: ModePermissive}
	data, err := os.ReadFile(path)
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

func (s *PermissionStore) SetMode(m PermissionMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	data, _ := yaml.Marshal(permsFile{Mode: string(m)})
	return os.WriteFile(s.path, data, 0o644)
}
