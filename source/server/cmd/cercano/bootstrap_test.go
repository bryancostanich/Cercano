package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestDetectEngine_Reachable(t *testing.T) {
	// Use a mock check function that returns no error (engine reachable)
	result := detectEngineWith(func(url string) error {
		return nil
	}, "http://localhost:11434")

	if !result.Available {
		t.Error("expected engine to be available")
	}
	if result.Name != "Ollama" {
		t.Errorf("expected engine name 'Ollama', got %q", result.Name)
	}
	if result.URL != "http://localhost:11434" {
		t.Errorf("expected URL 'http://localhost:11434', got %q", result.URL)
	}
}

func TestDetectEngine_Unreachable(t *testing.T) {
	result := detectEngineWith(func(url string) error {
		return &engineError{"connection refused"}
	}, "http://localhost:11434")

	if result.Available {
		t.Error("expected engine to be unavailable")
	}
}

func TestParseYesNo(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"y", true},
		{"Y", true},
		{"yes", true},
		{"YES", true},
		{"", true}, // default is yes
		{"\n", true},
		{"n", false},
		{"N", false},
		{"no", false},
		{"NO", false},
	}

	for _, tc := range tests {
		got := parseYesNo(tc.input)
		if got != tc.expected {
			t.Errorf("parseYesNo(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestPromptInstall_AutoYes(t *testing.T) {
	// With --install-engine flag, should not read from stdin at all
	var buf bytes.Buffer
	result := promptInstallEngine(&buf, nil, true)
	if !result {
		t.Error("expected true when --install-engine is set")
	}
}

func TestPromptInstall_Interactive(t *testing.T) {
	var outBuf bytes.Buffer
	inBuf := strings.NewReader("y\n")
	result := promptInstallEngine(&outBuf, inBuf, false)
	if !result {
		t.Error("expected true when user answers 'y'")
	}
	if !strings.Contains(outBuf.String(), "No AI engine backend was detected") {
		t.Error("expected engine-agnostic messaging in output")
	}
}

func TestPromptInstall_Declined(t *testing.T) {
	var outBuf bytes.Buffer
	inBuf := strings.NewReader("n\n")
	result := promptInstallEngine(&outBuf, inBuf, false)
	if result {
		t.Error("expected false when user answers 'n'")
	}
}

func TestInstallCommand_Darwin(t *testing.T) {
	cmd, args := ollamaInstallCommand("darwin", true)
	if cmd != "brew" {
		t.Errorf("expected 'brew' on darwin with homebrew, got %q", cmd)
	}
	if len(args) < 2 || args[0] != "install" || args[1] != "ollama" {
		t.Errorf("expected ['install', 'ollama'], got %v", args)
	}
}

func TestInstallCommand_DarwinNoBrew(t *testing.T) {
	cmd, _ := ollamaInstallCommand("darwin", false)
	if cmd != "" {
		t.Errorf("expected empty command when no homebrew on darwin, got %q", cmd)
	}
}

func TestInstallCommand_Linux(t *testing.T) {
	cmd, args := ollamaInstallCommand("linux", false)
	if cmd != "sh" {
		t.Errorf("expected 'sh' on linux, got %q", cmd)
	}
	if len(args) < 1 || !strings.Contains(args[1], "ollama.com/install.sh") {
		t.Errorf("expected ollama install script URL in args, got %v", args)
	}
}

func TestInstallCommand_Unsupported(t *testing.T) {
	cmd, _ := ollamaInstallCommand("windows", false)
	if cmd != "" {
		t.Errorf("expected empty command for unsupported platform, got %q", cmd)
	}
}

func TestStartCommand_Darwin(t *testing.T) {
	cmd, args := ollamaStartCommand("darwin", true)
	if cmd != "brew" {
		t.Errorf("expected 'brew' on darwin, got %q", cmd)
	}
	if len(args) < 3 || args[0] != "services" || args[1] != "start" || args[2] != "ollama" {
		t.Errorf("expected ['services', 'start', 'ollama'], got %v", args)
	}
}

func TestStartCommand_Linux(t *testing.T) {
	cmd, args := ollamaStartCommand("linux", false)
	if cmd != "ollama" {
		t.Errorf("expected 'ollama' on linux, got %q", cmd)
	}
	if len(args) != 1 || args[0] != "serve" {
		t.Errorf("expected ['serve'], got %v", args)
	}
}

func TestWaitForEngine_AlreadyRunning(t *testing.T) {
	calls := 0
	err := waitForEngine(func(url string) error {
		calls++
		return nil
	}, "http://localhost:11434", 3)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWaitForEngine_BecomesAvailable(t *testing.T) {
	calls := 0
	err := waitForEngine(func(url string) error {
		calls++
		if calls < 3 {
			return &engineError{"not ready"}
		}
		return nil
	}, "http://localhost:11434", 5)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestWaitForEngine_Timeout(t *testing.T) {
	err := waitForEngine(func(url string) error {
		return &engineError{"not ready"}
	}, "http://localhost:11434", 2)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

// Verify platform detection compiles and returns a valid value
func TestPlatformDetection(t *testing.T) {
	goos := runtime.GOOS
	if goos != "darwin" && goos != "linux" && goos != "windows" {
		t.Logf("running on unexpected platform: %s", goos)
	}
}
