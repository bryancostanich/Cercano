package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func TestHeaderOpenChipUsesConfiguredOpenModelNotLastServed(t *testing.T) {
	m := Model{
		styles:             theme.NewStyles(theme.Cracker()),
		palette:            theme.Cracker(),
		width:              120,
		openModel:          "qwen3-30b-a3b-instruct-2507-q4_k_m",
		lastModel:          "openai-responses",
		activeCloudProfile: "openai-responses",
	}

	out := stripAnsiCSI(m.renderHeader())
	if !strings.Contains(out, "o:qwen3-30b") {
		t.Fatalf("header should show configured open model in o: chip, got %q", out)
	}
	if strings.Contains(out, "o:openai-responses") {
		t.Fatalf("header o: chip should not show last-served cloud model, got %q", out)
	}
}

func TestHeaderOpenChipStripsRuntimeCatalogPrefix(t *testing.T) {
	m := Model{
		styles:    theme.NewStyles(theme.Cracker()),
		palette:   theme.Cracker(),
		width:     120,
		openModel: "llama_server:catalog:glm-4.5-air-q4_k_m",
	}

	out := stripAnsiCSI(m.renderHeader())
	if strings.Contains(out, "llama_server:catalog") {
		t.Fatalf("header should not show runtime catalog namespace, got %q", out)
	}
	if !strings.Contains(out, "o:glm-4.5-air") {
		t.Fatalf("header should show model name in o: chip, got %q", out)
	}
}

func TestHeaderOpenChipShowsDownloadingWhenRuntimeStatusDownloading(t *testing.T) {
	m := Model{
		styles:    theme.NewStyles(theme.Cracker()),
		palette:   theme.Cracker(),
		width:     120,
		openModel: "qwen3-30b-a3b-instruct-2507-q4_k_m",
		lastModel: "openai-responses",
		openRuntimeStatus: &agentclient.OpenRuntimeStatus{
			Runtime:     "mistralrs",
			Downloading: true,
		},
	}

	out := stripAnsiCSI(m.renderHeader())
	if !strings.Contains(out, "o:downloading") {
		t.Fatalf("header should show o:downloading while local model downloads, got %q", out)
	}
	if strings.Contains(out, "openai-responses") || strings.Contains(out, "qwen3-30b") {
		t.Fatalf("downloading state should take priority over model labels, got %q", out)
	}
}

func TestHeaderLongModelChipDoesNotDisplaceTitle(t *testing.T) {
	m := Model{
		styles:             theme.NewStyles(theme.Cracker()),
		palette:            theme.Cracker(),
		width:              115,
		sessionTitle:       "CERCANO - MORE UX",
		activeCloudProfile: "openai-responses",
		openModel:          "qwen3-30b-a3b-instruct-2507",
	}

	plain := stripAnsiCSI(m.renderHeader())
	if got := utf8.RuneCountInString(plain); got > m.width {
		t.Fatalf("header width = %d, want <= %d: %q", got, m.width, plain)
	}
	idx := strings.Index(plain, "CERCANO - MORE UX")
	if idx < 0 {
		t.Fatalf("title missing from header: %q", plain)
	}
	idxCol := utf8.RuneCountInString(plain[:idx])
	wantStart := (m.width-utf8.RuneCountInString("░▒▓ CERCANO - MORE UX ▓▒░"))/2 + utf8.RuneCountInString("░▒▓ ")
	if idxCol < wantStart-2 || idxCol > wantStart+6 {
		t.Fatalf("title start = %d, want near centered start %d; header=%q", idxCol, wantStart, plain)
	}
	if !strings.Contains(plain, "c:openai-responses") || !strings.Contains(plain, "o:") {
		t.Fatalf("header should preserve model chip prefixes while truncating labels, got %q", plain)
	}
}

func TestStatusDoesNotRenderOpenRuntimeMissingModelChip(t *testing.T) {
	m := Model{
		styles:  theme.NewStyles(theme.Cracker()),
		palette: theme.Cracker(),
		width:   120,
		openRuntimeStatus: &agentclient.OpenRuntimeStatus{
			Runtime: "mistralrs",
			Missing: "model",
			Message: "mistral.rs default model not downloaded",
		},
	}

	out := stripAnsiCSI(m.renderStatus())
	if strings.Contains(out, "model not downloaded") || strings.Contains(out, "o: downloading") || strings.Contains(out, "(F1)") {
		t.Fatalf("footer/status bar should not render local-runtime model state anymore, got %q", out)
	}
}

func TestConfigLoadedCloudChipPrefersModelDuringTransientCloudState(t *testing.T) {
	m := Model{
		styles:    theme.NewStyles(theme.Cracker()),
		palette:   theme.Cracker(),
		width:     120,
		openModel: "qwen3-30b-a3b-instruct-2507",
	}
	updated, _ := m.Update(configLoadedMsg{
		OpenModel:          "qwen3-30b-a3b-instruct-2507",
		CloudModel:         "claude-opus-5",
		ActiveCloudProfile: "claude",
		CloudConfigured:    false,
	})
	m = updated.(Model)

	out := stripAnsiCSI(m.renderHeader())
	if !strings.Contains(out, "c:opus-5") {
		t.Fatalf("header should show the cloud model, not the active profile name, got %q", out)
	}
	if strings.Contains(out, "c:claude") {
		t.Fatalf("header c: chip should not prefer active profile over model, got %q", out)
	}
	if !strings.Contains(out, "o:qwen3-30b") {
		t.Fatalf("header should still show open chip, got %q", out)
	}
}
