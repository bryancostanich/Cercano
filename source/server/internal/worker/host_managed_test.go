package worker

import "testing"

// TestRuntimeIsHostManaged pins the worker's open-routing predicate: host-only
// runtimes (llama_server, mistralrs) must proxy to the host; a URL-reachable
// Ollama-style runtime (or an unknown one) routes to a direct client.
func TestRuntimeIsHostManaged(t *testing.T) {
	cases := map[string]bool{
		"llama_server": true,
		"mistralrs":    true,
		"ollama":       false,
		"":             false,
		"something":    false,
	}
	for runtime, want := range cases {
		if got := runtimeIsHostManaged(runtime); got != want {
			t.Errorf("runtimeIsHostManaged(%q) = %v, want %v", runtime, got, want)
		}
	}
}
