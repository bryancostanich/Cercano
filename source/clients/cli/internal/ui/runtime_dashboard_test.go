package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/server/pkg/agentclient"
)

func TestIsRuntimeDashboardKey(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{
			name: "super m",
			msg:  tea.KeyPressMsg{Code: 'm', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "meta m",
			msg:  tea.KeyPressMsg{Code: 'm', Mod: tea.ModMeta},
			want: true,
		},
		{
			name: "alternate base code",
			msg:  tea.KeyPressMsg{Code: 'µ', BaseCode: 'm', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "plain m",
			msg:  tea.KeyPressMsg{Code: 'm'},
			want: false,
		},
		{
			name: "super c",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRuntimeDashboardKey(tc.msg); got != tc.want {
				t.Fatalf("isRuntimeDashboardKey = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRuntimeRowsFromSnapshotShowsRuntimeDashboardData(t *testing.T) {
	rows := runtimeRowsFromSnapshot(runtimeDashboardSnapshot{
		Config: &agentclient.Config{
			LocalRuntime:   "llama_server",
			LocalModel:     "qwen.gguf",
			EmbeddingModel: "nomic-embed-text",
			CloudProvider:  "openai",
			CloudModel:     "gpt-test",
			CloudBaseURL:   "https://llm.example.test/v1",
		},
		Status: &agentclient.RuntimeStatus{
			Endpoints: []agentclient.RuntimeEndpoint{{
				ID:          "cloud:openai",
				Kind:        "openai_compatible",
				DisplayName: "openai proxy",
				BaseURL:     "https://llm.example.test/v1",
				Scope:       "remote",
				State:       "unknown",
				ActiveRoles: []string{"cloud_fallback"},
				Models:      []string{"gpt-test"},
				AuthState:   "configured",
			}},
			Models: []agentclient.RuntimeModel{{
				ID:            "/models/qwen.gguf",
				DisplayName:   "qwen",
				Runtime:       "llama_server",
				DownloadState: "downloaded",
				SupportsChat:  true,
				SizeBytes:     2 * 1024 * 1024 * 1024,
			}},
			Instances: []agentclient.RuntimeInstance{{
				ID:        "llama-1234567890",
				Runtime:   "llama_server",
				ModelID:   "/models/running.gguf",
				State:     "running",
				PID:       4242,
				Endpoint:  "http://127.0.0.1:18080",
				StartedAt: time.Now().Add(-2 * time.Minute),
			}},
			Logs: []agentclient.RuntimeLogEntry{{
				Timestamp: time.Now(),
				Source:    "llama-server",
				Level:     "info",
				RuntimeID: "llama_server",
				ModelID:   "/models/running.gguf",
				Message:   "server ready",
			}},
		},
	})

	assertRowContains(t, rows, "external endpoints", "1 endpoint")
	assertRowContains(t, rows, "endpoint openai proxy", "openai_compatible")
	assertRowContains(t, rows, "downloaded models", "1 model")
	assertRowContains(t, rows, "model qwen", "llama_server")
	assertRowContains(t, rows, "start qwen.gguf", "llama_server")
	assertRowContains(t, rows, "runtime processes", "1 process")
	assertRowContains(t, rows, "process running.gguf", "running")
	assertRowContains(t, rows, "stop running.gguf", "llama_server")
	assertRowContains(t, rows, "restart running.gguf", "llama_server")
	assertRowContains(t, rows, "recent logs", "1 entry")
	assertAnyRowContains(t, rows, "server ready")
}

func TestRuntimeDashboardActionRoundTrip(t *testing.T) {
	want := runtimeDashboardAction{
		Kind:       runtimeActionRestart,
		Runtime:    "llama_server",
		ModelID:    "/models/qwen.gguf",
		InstanceID: "llama-abc",
	}
	got, err := parseRuntimeDashboardAction(encodeRuntimeDashboardAction(want))
	if err != nil {
		t.Fatalf("parse action: %v", err)
	}
	if got != want {
		t.Fatalf("action = %+v, want %+v", got, want)
	}
}

func assertRowContains(t *testing.T, rows []overlay.Row, label, value string) {
	t.Helper()
	for _, row := range rows {
		if row.Label == label && strings.Contains(row.Value, value) {
			return
		}
	}
	t.Fatalf("missing row label %q containing value %q in %#v", label, value, rows)
}

func assertAnyRowContains(t *testing.T, rows []overlay.Row, value string) {
	t.Helper()
	for _, row := range rows {
		if strings.Contains(row.Value, value) {
			return
		}
	}
	t.Fatalf("missing any row containing value %q in %#v", value, rows)
}
