package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestStripModelIDPrefix(t *testing.T) {
	cases := map[string]string{
		"llama_server:catalog:qwen2.5-coder-1.5b-q4_k_m": "qwen2.5-coder-1.5b-q4_k_m",
		"llama_server:online:qwen2.5-coder":              "qwen2.5-coder",
		"llama_server:f1f3470efd49":                      "f1f3470efd49",
		"ollama:qwen3-coder-next":                        "qwen3-coder-next",
		// Ollama-style name:tag refs are not namespaced IDs — untouched.
		"qwen2.5-coder:7b": "qwen2.5-coder:7b",
		"plain-name":       "plain-name",
	}
	for in, want := range cases {
		if got := stripModelIDPrefix(in); got != want {
			t.Errorf("stripModelIDPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelValue_OmitsRedundantNoise(t *testing.T) {
	model := agentclient.RuntimeModel{
		ID:            "llama_server:catalog:test",
		Runtime:       "llama_server",
		Family:        "qwen",
		Quantization:  "Q4_K_M",
		SizeBytes:     1 << 30,
		DownloadState: "downloaded",
		RuntimeState:  "stopped",
	}
	got := modelValue(model, agentclient.RuntimeInstance{})
	for _, noise := range []string{"llama_server", "downloaded", "stopped", "qwen"} {
		if strings.Contains(got, noise) {
			t.Errorf("modelValue %q still contains %q", got, noise)
		}
	}
	for _, want := range []string{"1.0 GB", "Q4_K_M"} {
		if !strings.Contains(got, want) {
			t.Errorf("modelValue %q missing %q", got, want)
		}
	}
}

func TestModelValue_IncludesRAMWhenWarmed(t *testing.T) {
	model := agentclient.RuntimeModel{
		SizeBytes:        4683074048,
		Quantization:     "Q4_K_M",
		DownloadState:    "downloaded",
		KVBytesPerToken:  57344,
		MaxContextTokens: 32768,
	}
	got := modelValue(model, agentclient.RuntimeInstance{})
	if !strings.Contains(got, "RAM @8k") {
		t.Errorf("modelValue %q missing RAM estimate", got)
	}
}

func TestModelValue_KeepsNotableState(t *testing.T) {
	downloading := agentclient.RuntimeModel{SizeBytes: 1 << 30, DownloadState: "downloading"}
	if got := modelValue(downloading, agentclient.RuntimeInstance{}); !strings.Contains(got, "downloading") {
		t.Errorf("downloading state dropped: %q", got)
	}
	running := agentclient.RuntimeModel{SizeBytes: 1 << 30, DownloadState: "downloaded"}
	inst := agentclient.RuntimeInstance{ID: "i1", State: "running"}
	if got := modelValue(running, inst); !strings.Contains(got, "process:running") {
		t.Errorf("running process dropped: %q", got)
	}
}
