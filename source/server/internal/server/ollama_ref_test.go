package server

import "testing"

func TestNormalizeOllamaRef(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Bare family names (what online catalog entries carry) get
		// the :latest default so the OCI resolver accepts them.
		{"qwen2.5-coder", "qwen2.5-coder:latest"},
		{"llama3.2", "llama3.2:latest"},
		// Explicit tags pass through untouched.
		{"qwen2.5-coder:7b", "qwen2.5-coder:7b"},
		{"qwen2.5-coder:latest", "qwen2.5-coder:latest"},
		// Empty means "no online-catalog enrolment" and must stay
		// empty — appending :latest here would enroll a phantom model.
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeOllamaRef(tc.in); got != tc.want {
			t.Errorf("normalizeOllamaRef(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
