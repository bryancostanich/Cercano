package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/dispatch"
	openaiadapter "cercano/source/server/internal/llm/openai"
)

// TestLiveOpenModelDispatch_ReadToolSmoke is an opt-in smoke test for the real
// local/open-model delegation path. It is intentionally skipped by default: it
// needs a local OpenAI-compatible server (for example llama-server) with a
// tool-capable model loaded, and model behavior is inherently less deterministic
// than the hermetic fixtures in the rest of this package.
//
// Example:
//
//	CERCANO_LIVE_OPEN_MODEL_TEST=1 \
//	CERCANO_LIVE_OPEN_MODEL_BASE_URL=http://127.0.0.1:8080/v1 \
//	CERCANO_LIVE_OPEN_MODEL_MODEL=glm-4.5-air \
//	go test ./internal/server/ -run TestLiveOpenModelDispatch_ReadToolSmoke -count=1 -v
func TestLiveOpenModelDispatch_ReadToolSmoke(t *testing.T) {
	if os.Getenv("CERCANO_LIVE_OPEN_MODEL_TEST") != "1" {
		t.Skip("set CERCANO_LIVE_OPEN_MODEL_TEST=1 plus CERCANO_LIVE_OPEN_MODEL_BASE_URL and CERCANO_LIVE_OPEN_MODEL_MODEL to run live local/open-model dispatch smoke")
	}
	baseURL := strings.TrimSpace(os.Getenv("CERCANO_LIVE_OPEN_MODEL_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("CERCANO_LIVE_OPEN_MODEL_MODEL"))
	if baseURL == "" || model == "" {
		t.Skip("live open-model smoke requires CERCANO_LIVE_OPEN_MODEL_BASE_URL and CERCANO_LIVE_OPEN_MODEL_MODEL")
	}
	apiKey := os.Getenv("CERCANO_LIVE_OPEN_MODEL_API_KEY")
	if apiKey == "" {
		apiKey = "local"
	}

	dir := t.TempDir()
	sentinel := "CERCANO-LIVE-DISPATCH-SENTINEL-42"
	p := filepath.Join(dir, "sentinel.txt")
	if err := os.WriteFile(p, []byte(sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	capReg := capabilities.NewRegistry(capabilities.Services{})
	builtins.Register(capReg)
	srv, _ := newServerWithStore(t)
	srv.SetToolRegistry(agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms()))
	perms, err := agent.LoadPermissionStore(filepath.Join(t.TempDir(), "perms.yaml"))
	if err != nil {
		t.Fatalf("LoadPermissionStore: %v", err)
	}
	srv.SetPermissions(perms, nil)

	provider := openaiadapter.NewClient(openaiadapter.Config{BaseURL: baseURL, APIKey: apiKey, Model: model})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var notes []string
	out := captureLog(t, func() {
		res, err := srv.runAgenticDispatch(ctx,
			dispatch.Spec{
				Mode:    dispatch.Agentic,
				Task:    "Use the Read tool to open sentinel.txt and report the exact sentinel token on the first line. You must call Read.",
				Tools:   []string{"Read"},
				WorkDir: dir,
				Emit: func(ev agenttools.ProgressEvent) {
					notes = append(notes, ev.Text)
				},
			},
			dispatch.Selection{Provider: provider, IsCloud: false}, model)
		if err != nil {
			t.Fatalf("runAgenticDispatch live smoke: %v", err)
		}
		if strings.TrimSpace(res.Text) == "" {
			t.Fatal("live dispatch returned empty text")
		}
		if !strings.Contains(res.Text, sentinel) {
			t.Fatalf("live dispatch text did not contain sentinel %q:\n%s", sentinel, res.Text)
		}
		if res.IsCloud {
			t.Fatalf("live open-model smoke should be local/open, got cloud route: %+v", res)
		}
	})

	if !strings.Contains(out, "called=[Read]") {
		t.Fatalf("expected live dispatch log to record called=[Read]; logs:\n%s\nprogress:\n%s", out, strings.Join(notes, "\n"))
	}
}
