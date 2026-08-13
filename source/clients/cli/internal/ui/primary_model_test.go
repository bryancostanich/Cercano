package ui

import "testing"

func TestPrimaryModelName(t *testing.T) {
	const (
		open  = "qwen3-coder"
		cloud = "claude-opus-5-0"
	)
	cases := []struct {
		name            string
		locus           string
		cloudConfigured bool
		want            string
	}{
		{"cloud_primary configured → cloud", "cloud_primary", true, cloud},
		{"cloud_only configured → cloud", "cloud_only", true, cloud},
		{"cloud_primary but cloud absent → open fallback", "cloud_primary", false, open},
		{"open_primary → open", "open_primary", true, open},
		{"open_only → open", "open_only", true, open},
		{"unset locus → open", "", true, open},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := primaryModelName(open, cloud, tc.locus, tc.cloudConfigured)
			if got != tc.want {
				t.Errorf("primaryModelName(%q, %q, %q, %v) = %q, want %q",
					open, cloud, tc.locus, tc.cloudConfigured, got, tc.want)
			}
		})
	}
}

func TestPrimaryModelName_CloudSideFallsBackWhenNoOpen(t *testing.T) {
	// cloud_only with cloud unconfigured and no local model: last-resort to the
	// cloud name rather than returning empty (empty would blank the banner).
	if got := primaryModelName("", "claude-opus-5-0", "cloud_only", false); got != "claude-opus-5-0" {
		t.Errorf("want last-resort cloud name, got %q", got)
	}
}
