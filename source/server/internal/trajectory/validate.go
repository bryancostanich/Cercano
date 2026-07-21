package trajectory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidateBundle(root string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	trajPath := filepath.Join(rootAbs, "trajectory.json")
	data, err := os.ReadFile(trajPath)
	if err != nil {
		return fmt.Errorf("read trajectory.json: %w", err)
	}
	var tr Trajectory
	if err := json.Unmarshal(data, &tr); err != nil {
		return fmt.Errorf("parse trajectory.json: %w", err)
	}
	if tr.SchemaVersion != ATIFVersion {
		return fmt.Errorf("schema_version = %q, want %q", tr.SchemaVersion, ATIFVersion)
	}
	for i, st := range tr.Steps {
		if st.StepID != i+1 {
			return fmt.Errorf("step_id at index %d = %d, want %d", i, st.StepID, i+1)
		}
	}
	manPath := filepath.Join(rootAbs, "manifest.json")
	if _, err := os.Stat(manPath); err == nil {
		b, _ := os.ReadFile(manPath)
		var m Manifest
		if json.Unmarshal(b, &m) == nil {
			for _, a := range m.Artifacts {
				if err := validateRel(rootAbs, a.Path); err != nil {
					return err
				}
				if _, err := os.Stat(filepath.Join(rootAbs, filepath.FromSlash(a.Path))); err != nil {
					return fmt.Errorf("manifest artifact missing %s: %w", a.Path, err)
				}
			}
			for _, s := range m.Subagents {
				if err := validateRel(rootAbs, s.Path); err != nil {
					return err
				}
				if _, err := os.Stat(filepath.Join(rootAbs, filepath.FromSlash(s.Path))); err != nil {
					return fmt.Errorf("subagent trajectory missing %s: %w", s.Path, err)
				}
			}
		}
	}
	return nil
}
func validateRel(root, rel string) error {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return fmt.Errorf("unsafe bundle path %q", rel)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(abs, root) {
		return fmt.Errorf("path escapes bundle root: %q", rel)
	}
	return nil
}
