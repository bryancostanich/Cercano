package main

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestAppleTerminalTruecolor(t *testing.T) {
	cases := []struct {
		name     string
		env      []string
		detected colorprofile.Profile
		want     bool
	}{
		{"apple + truecolor → clamp", []string{"TERM_PROGRAM=Apple_Terminal"}, colorprofile.TrueColor, true},
		{"apple + 256 → leave", []string{"TERM_PROGRAM=Apple_Terminal"}, colorprofile.ANSI256, false},
		{"apple + notty (pipe) → leave", []string{"TERM_PROGRAM=Apple_Terminal"}, colorprofile.NoTTY, false},
		{"ghostty + truecolor → leave", []string{"TERM_PROGRAM=ghostty"}, colorprofile.TrueColor, false},
		{"iterm + truecolor → leave", []string{"TERM_PROGRAM=iTerm.app"}, colorprofile.TrueColor, false},
		{"no term_program → leave", []string{"COLORTERM=truecolor"}, colorprofile.TrueColor, false},
		{"nil env → leave", nil, colorprofile.TrueColor, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appleTerminalTruecolor(tc.env, tc.detected); got != tc.want {
				t.Fatalf("appleTerminalTruecolor(%v, %v) = %v, want %v", tc.env, tc.detected, got, tc.want)
			}
		})
	}
}

func TestEnvValue(t *testing.T) {
	env := []string{"FOO=bar", "TERM_PROGRAM=Apple_Terminal", "EMPTY="}
	if got := envValue(env, "TERM_PROGRAM"); got != "Apple_Terminal" {
		t.Fatalf("TERM_PROGRAM = %q, want Apple_Terminal", got)
	}
	if got := envValue(env, "EMPTY"); got != "" {
		t.Fatalf("EMPTY = %q, want empty", got)
	}
	if got := envValue(env, "MISSING"); got != "" {
		t.Fatalf("MISSING = %q, want empty", got)
	}
}
