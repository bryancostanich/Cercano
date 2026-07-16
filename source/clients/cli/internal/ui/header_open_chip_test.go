package ui

import (
	"strings"
	"testing"

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
