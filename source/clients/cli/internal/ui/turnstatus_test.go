package ui

import (
	"testing"
	"time"
)

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{900 * time.Millisecond, "0s"},
		{4200 * time.Millisecond, "4s"},
		{59*time.Second + 900*time.Millisecond, "59s"},
		{60 * time.Second, "1m00s"},
		{65 * time.Second, "1m05s"},
		{334 * time.Second, "5m34s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestTurnStatusLine(t *testing.T) {
	cases := []struct {
		name, activity string
		elapsed        time.Duration
		tokOut         int
		model          string
		isCloud        bool
		want           string
	}{
		{"thinking local no tokens", "thinking", time.Second, 0, "qwen3-coder", false,
			"thinking · 1s · qwen3-coder (local)"},
		{"running tool cloud", "running Bash", 4 * time.Second, 0, "claude-opus", true,
			"running Bash · 4s · claude-opus (cloud)"},
		{"writing with tokens", "writing", 4 * time.Second, 312, "qwen3-coder", false,
			"writing · 4s · 312 tok↑ · qwen3-coder (local)"},
		{"no engine yet", "thinking", time.Second, 0, "", false,
			"thinking · 1s"},
	}
	for _, c := range cases {
		if got := turnStatusLine(c.activity, c.elapsed, c.tokOut, c.model, c.isCloud); got != c.want {
			t.Errorf("%s: turnStatusLine = %q, want %q", c.name, got, c.want)
		}
	}
}
