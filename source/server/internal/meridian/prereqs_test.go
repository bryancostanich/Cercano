package meridian

import (
	"errors"
	"strings"
	"testing"
)

func TestDetectPrereqs_AllPresent(t *testing.T) {
	lookPath := func(name string) (string, error) {
		switch name {
		case "node":
			return "/opt/homebrew/bin/node", nil
		case "claude":
			return "/opt/homebrew/bin/claude", nil
		}
		return "", errors.New("not found")
	}
	run := func(name string, arg ...string) ([]byte, error) {
		return []byte("v22.10.0\n"), nil
	}
	p := detectPrereqsWith(lookPath, run)
	if !p.NodeOK {
		t.Errorf("NodeOK = false, want true")
	}
	if p.NodeVersion != "v22.10.0" {
		t.Errorf("NodeVersion = %q, want v22.10.0", p.NodeVersion)
	}
	if !p.ClaudeOK {
		t.Errorf("ClaudeOK = false, want true")
	}
	if len(p.MissingNotes) != 0 {
		t.Errorf("MissingNotes = %v, want empty", p.MissingNotes)
	}
}

func TestDetectPrereqs_NodeTooOld(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "node" {
			return "/usr/bin/node", nil
		}
		return "", errors.New("not found")
	}
	run := func(name string, arg ...string) ([]byte, error) {
		return []byte("v18.19.0\n"), nil
	}
	p := detectPrereqsWith(lookPath, run)
	if p.NodeOK {
		t.Errorf("NodeOK = true for v18, want false (min is v22)")
	}
	joined := strings.Join(p.MissingNotes, " | ")
	if !strings.Contains(joined, "too old") {
		t.Errorf("expected 'too old' note, got: %s", joined)
	}
}

func TestDetectPrereqs_NodeMissing(t *testing.T) {
	lookPath := func(name string) (string, error) {
		return "", errors.New("not found")
	}
	run := func(name string, arg ...string) ([]byte, error) {
		t.Fatalf("should not run when node missing")
		return nil, nil
	}
	p := detectPrereqsWith(lookPath, run)
	if p.NodeOK {
		t.Errorf("NodeOK = true, want false when node missing")
	}
	if p.ClaudeOK {
		t.Errorf("ClaudeOK = true, want false when claude missing")
	}
	if len(p.MissingNotes) == 0 || !strings.Contains(p.MissingNotes[0], "Node.js") {
		t.Errorf("expected Node missing note, got: %v", p.MissingNotes)
	}
}

func TestDetectPrereqs_ClaudeMissingIsNotFatal(t *testing.T) {
	// Claude CLI absence is recoverable via `npx`, so we don't surface it
	// as a missing-dep note. Only NodeOK matters for "can we run meridian".
	lookPath := func(name string) (string, error) {
		if name == "node" {
			return "/opt/homebrew/bin/node", nil
		}
		return "", errors.New("not found")
	}
	run := func(name string, arg ...string) ([]byte, error) {
		return []byte("v22.0.0"), nil
	}
	p := detectPrereqsWith(lookPath, run)
	if !p.NodeOK {
		t.Errorf("NodeOK = false, want true (node present, version OK)")
	}
	if p.ClaudeOK {
		t.Errorf("ClaudeOK = true, want false (claude not on PATH)")
	}
	if len(p.MissingNotes) != 0 {
		t.Errorf("MissingNotes = %v, want empty (claude is npx-fallbackable)", p.MissingNotes)
	}
}

func TestParseNodeMajor(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"v22.10.0", 22, true},
		{"22.10.0", 22, true},
		{"v18", 18, true},
		{" v24.0.0 \n", 24, true},
		{"vX.Y.Z", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseNodeMajor(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseNodeMajor(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
