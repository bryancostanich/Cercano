package slash

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestFormatCloudProfilesEmpty(t *testing.T) {
	got := formatCloudProfiles(nil, "")
	if got != "cloud: no profiles configured" {
		t.Errorf("unexpected output for empty profiles: %q", got)
	}
}

func TestFormatCloudProfilesMarksActive(t *testing.T) {
	profiles := []agentclient.CloudProfileInfo{
		{Name: "alpha", Flavor: "openai", Model: "gpt-4o", HasKey: true},
		{Name: "beta", Flavor: "anthropic", Model: "claude-3-5-sonnet", HasKey: false},
	}
	got := formatCloudProfiles(profiles, "beta")

	// Active row must contain "*"
	lines := strings.Split(got, "\n")
	var betaLine string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "beta") {
			betaLine = l
		}
	}
	if betaLine == "" {
		t.Fatal("beta row not found in output")
	}
	if !strings.Contains(betaLine, "*") {
		t.Errorf("beta row missing active marker: %q", betaLine)
	}

	// Inactive row must not contain "*"
	var alphaLine string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "alpha") {
			alphaLine = l
		}
	}
	if strings.Contains(alphaLine, "*") {
		t.Errorf("alpha row should not have active marker: %q", alphaLine)
	}
}

func TestFormatCloudProfilesKeyIndicator(t *testing.T) {
	profiles := []agentclient.CloudProfileInfo{
		{Name: "withkey", Flavor: "openai", Model: "gpt-4o", HasKey: true},
		{Name: "nokey", Flavor: "openai", Model: "gpt-4o", HasKey: false},
	}
	got := formatCloudProfiles(profiles, "withkey")
	lines := strings.Split(got, "\n")

	for _, l := range lines {
		if strings.Contains(l, "withkey") {
			if !strings.Contains(l, "✓") {
				t.Errorf("withkey row missing ✓: %q", l)
			}
		}
		if strings.Contains(l, "nokey") {
			if !strings.Contains(l, "✗") {
				t.Errorf("nokey row missing ✗: %q", l)
			}
		}
	}
}

func TestMaskKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"sk-abc123", "sk-a..."},
		{"abcd", "***"},
		{"abc", "***"},
		{"sk-12345678", "sk-1..."},
	}
	for _, tc := range cases {
		got := maskKey(tc.input)
		if got != tc.want {
			t.Errorf("maskKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
