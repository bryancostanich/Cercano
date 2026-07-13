package localruntime

import "testing"

func TestModelDirName(t *testing.T) {
	cases := map[string]string{
		"unsloth/Qwen3-14B-GGUF": "unsloth-Qwen3-14B-GGUF",
		"qwen3-14b-q4_k_m":       "qwen3-14b-q4_k_m",
		"Qwen/Qwen3-Next":        "Qwen-Qwen3-Next",
		"a/b:c d":                "a-b-c-d",
		"  spaced  ":             "spaced",
		"...dots...":             "dots",
		"":                       "model",
		"/////":                  "model",
	}
	for in, want := range cases {
		if got := ModelDirName(in); got != want {
			t.Errorf("ModelDirName(%q) = %q, want %q", in, got, want)
		}
	}
}
