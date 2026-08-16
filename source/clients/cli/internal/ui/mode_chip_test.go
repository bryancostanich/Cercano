package ui

import (
	"strings"
	"testing"
)

// visible strips ANSI SGR sequences so we can assert on the chip's plain text.
func visible(s string) string { return ansiRE.ReplaceAllString(s, "") }

// TestModeChip_MergesPlanningAndPermission is the load-bearing test for the
// footer mode chip: planning mode and the permission mode are orthogonal axes
// rendered together, pipe-separated, with planning first.
func TestModeChip_MergesPlanningAndPermission(t *testing.T) {
	m := New(nil, false)

	cases := []struct {
		name       string
		perm       string
		profile    string
		wantSubstr string // "" means the chip should be empty
	}{
		{"nothing known yet", "", "", ""},
		{"permission only", "bypass", "", "mode: bypass"},
		{"permission only permissive", "permissive", "", "mode: permissive"},
		{"planning only", "", "plan", "mode: planning"},
		{"autonomous only", "", "autonomous", "mode: autonomous"},
		{"both merged", "bypass", "plan", "mode: planning | bypass"},
		{"autonomous merged", "permissive", "autonomous", "mode: autonomous | permissive"},
		{"both merged strict", "strict", "plan", "mode: planning | strict"},
		{"autonomous merged strict", "strict", "autonomous", "mode: autonomous | strict"},
		{"default profile does not add planning", "permissive", "", "mode: permissive"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.permissionMode = tc.perm
			m.sessionProfile = tc.profile
			got := visible(m.renderPermissionModeChip())

			if tc.wantSubstr == "" {
				if got != "" {
					t.Fatalf("expected empty chip, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSubstr) {
				t.Fatalf("chip = %q, want it to contain %q", got, tc.wantSubstr)
			}
		})
	}
}

// TestModeChip_PlanningFirstWithPipe pins the exact ordering and separator so a
// refactor can't silently flip "planning | bypass" to "bypass | planning".
func TestModeChip_PlanningFirstWithPipe(t *testing.T) {
	m := New(nil, false)
	m.permissionMode = "bypass"
	m.sessionProfile = "plan"

	got := visible(m.renderPermissionModeChip())
	pi := strings.Index(got, "planning")
	bi := strings.Index(got, "bypass")
	if pi < 0 || bi < 0 {
		t.Fatalf("chip %q must mention both planning and bypass", got)
	}
	if pi > bi {
		t.Fatalf("planning must come before the permission mode; got %q", got)
	}
	if !strings.Contains(got, "planning | bypass") {
		t.Fatalf("axes must be pipe-separated; got %q", got)
	}
}

// TestNormalizeProfile collapses the unrestricted posture so the chip only
// receives named non-default profiles.
func TestNormalizeProfile(t *testing.T) {
	if got := normalizeProfile("default"); got != "" {
		t.Fatalf("normalizeProfile(default) = %q, want empty", got)
	}
	if got := normalizeProfile("plan"); got != "plan" {
		t.Fatalf("normalizeProfile(plan) = %q, want plan", got)
	}
	if got := normalizeProfile("autonomous"); got != "autonomous" {
		t.Fatalf("normalizeProfile(autonomous) = %q, want autonomous", got)
	}
	if got := normalizeProfile(""); got != "" {
		t.Fatalf("normalizeProfile(\"\") = %q, want empty", got)
	}
}
