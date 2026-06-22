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

func (s *PermissionStore) Mode() PermissionMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *PermissionStore) SetMode(m PermissionMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	data, _ := yaml.Marshal(permsFile{Mode: string(m)})
	return os.WriteFile(s.path, data, 0o644)
}
