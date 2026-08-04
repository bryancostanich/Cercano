package llm

import "testing"

func TestDetectContextOverflow(t *testing.T) {
	cases := []struct {
		name        string
		msg         string
		wantOK      bool
		wantUsed    int
		wantLimit   int
	}{
		{
			name:      "llama-server with counts",
			msg:       "request (21156 tokens) exceeds the available context size (16384 tokens)",
			wantOK:    true,
			wantUsed:  21156,
			wantLimit: 16384,
		},
		{
			name:   "opaque cloud openai-compat",
			msg:    "Context size has been exceeded.",
			wantOK: true,
		},
		{
			name:   "openai classic max context length",
			msg:    "This model's maximum context length is 8192 tokens, however you requested 9000 tokens",
			wantOK: true,
		},
		{
			name:   "unrelated invalid request",
			msg:    "The model does not exist.",
			wantOK: false,
		},
		{
			name:   "empty",
			msg:    "",
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, used, limit := DetectContextOverflow(c.msg)
			if ok != c.wantOK {
				t.Fatalf("overflow = %v, want %v", ok, c.wantOK)
			}
			if used != c.wantUsed || limit != c.wantLimit {
				t.Errorf("counts = (%d,%d), want (%d,%d)", used, limit, c.wantUsed, c.wantLimit)
			}
		})
	}
}
