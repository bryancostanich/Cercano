package llamaserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	llamaengine "cercano/source/server/internal/engine/llamaserver"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// This helper is an HTTP fixture process, NOT a real model runtime. The launch
// command receives and reports the actual flags chosen by Provider.Start.
func TestContextIntegrationHelper(t *testing.T) {
	if os.Getenv("CERCANO_CONTEXT_TEST_CHILD") != "1" {
		return
	}
	port, n := 0, 0
	for i, a := range os.Args {
		if i+1 >= len(os.Args) {
			continue
		}
		switch a {
		case "--port":
			port, _ = strconv.Atoi(os.Args[i+1])
		case "--ctx-size":
			n, _ = strconv.Atoi(os.Args[i+1])
		}
	}
	if port == 0 || n <= 0 {
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"default_generation_settings":{"n_ctx":%d},"total_slots":1}`, n)
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `[{"n_ctx":%d}]`, n) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestAutomaticConfigToLaunchConfirmationAndBudget(t *testing.T) {
	for _, override := range []int{0, 8192, 65536} {
		t.Run(strconv.Itoa(override), func(t *testing.T) { runContextIntegration(t, override) })
	}
}

func runContextIntegration(t *testing.T, override int) {
	t.Helper()
	dir := t.TempDir()
	path := writeIdentityGGUF(t, dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fake-llama-server")
	// POSIX shell fixture is only used in this Darwin/Linux-oriented runtime suite.
	if err := os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run '^TestContextIntegrationHelper$' -- \"$@\"\n", exe)), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CERCANO_CONTEXT_TEST_CHILD", "1")
	cfg := config.Defaults()
	cfg.LlamaServer = config.LlamaServerConfig{Enabled: true, Binary: binary, ModelDirs: []string{dir}, Host: "127.0.0.1", ReadinessTimeout: "20s"}
	if override > 0 {
		cfg.LlamaServer.ContextSize = &override
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if override == 0 && cfg.LlamaServer.ContextSize != nil {
		t.Fatal("save/reload invented context override")
	}
	p := NewProvider(cfg.LlamaServer)
	// Clean up even when Start returns a still-loading error. The fixture may
	// be delayed while other Go test packages are saturating the machine.
	t.Cleanup(func() {
		p.mu.RLock()
		ids := make([]string, 0, len(p.running))
		for id := range p.running {
			ids = append(ids, id)
		}
		p.mu.RUnlock()
		for _, id := range ids {
			_ = p.Stop(context.Background(), id)
		}
	})
	p.totalRAM = func() int64 { return 64 << 30 }
	p.nonEvictable = func() (int64, bool) { return 1 << 30, true }
	manager := localruntime.NewManager()
	manager.RegisterProvider(p)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	instance, err := manager.Start(ctx, localruntime.StartRequest{Runtime: "llama_server", ModelID: path})
	if err != nil {
		logs, _ := manager.Logs(context.Background(), localruntime.LogRequest{Tail: 20})
		t.Fatalf("fixture startup: %v; logs=%+v", err, logs)
	}
	defer manager.Stop(context.Background(), localruntime.StopRequest{InstanceID: instance.ID})
	want, source := 2048, "gguf"
	if override > 0 {
		want, source = override, "config"
	}
	if instance.Context.PlannedTokens != want || instance.Context.PlannedSource != source || instance.Context.ConfirmedTokens != want {
		t.Fatalf("allocation/confirmation=%+v", instance.Context)
	}
	var accounting agent.LoopEvent
	provider := llamaengine.NewLLMProvider(llamaengine.NewEngine(manager))
	_, err = agent.RunToolLoop(ctx, agent.ToolLoopInput{Provider: provider, Registry: agenttools.NewRegistry(), Model: path, UserInput: "hello", MaxTokensPerTurn: 128, ContextWindow: 131072, ContextWindowKnown: true, EventSink: func(ev agent.LoopEvent) {
		if ev.Kind == agent.LoopRequestAccounting {
			accounting = ev
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !accounting.ContextWindowKnown || accounting.ContextWindow != want {
		t.Fatalf("budget diverged from actual launch: %+v", accounting)
	}
}
