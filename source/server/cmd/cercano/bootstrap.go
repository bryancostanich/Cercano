package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// engineError is a simple error type for engine detection failures.
type engineError struct {
	msg string
}

func (e *engineError) Error() string { return e.msg }

// engineCheckFunc is a function that checks if an engine is reachable at a URL.
type engineCheckFunc func(url string) error

// engineDetectionResult holds the result of an engine detection check.
type engineDetectionResult struct {
	Name      string
	URL       string
	Available bool
}

// detectEngineWith checks if an engine is reachable using the provided check function.
func detectEngineWith(check engineCheckFunc, url string) engineDetectionResult {
	err := check(url)
	return engineDetectionResult{
		Name:      "Ollama",
		URL:       url,
		Available: err == nil,
	}
}

// parseYesNo parses a yes/no response. Empty or whitespace-only input defaults to yes.
func parseYesNo(input string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	if trimmed == "" {
		return true
	}
	return trimmed == "y" || trimmed == "yes"
}

// engineSetupChoice is the outcome of promptEngineSetupChoice: how the user
// wants to proceed when no AI engine was detected at the configured URL.
type engineSetupChoice int

const (
	engineChoiceInstallLocal engineSetupChoice = iota
	engineChoiceRemote
	engineChoiceSkip
)

// promptEngineSetupChoice asks how the user wants to configure a local AI
// engine when none was detected at cfg.OllamaURL: install Ollama locally,
// point at an existing Ollama server elsewhere on the network, or skip AI
// setup for now. Returns the choice and, for engineChoiceRemote, the raw URL
// string the user typed (unvalidated — the caller validates so it can decide
// how to react to a bad URL).
// If autoInstall is true, skips the prompt and returns engineChoiceInstallLocal.
func promptEngineSetupChoice(out io.Writer, in io.Reader, autoInstall bool) (engineSetupChoice, string) {
	if autoInstall {
		return engineChoiceInstallLocal, ""
	}

	fmt.Fprintln(out, "  No AI engine backend was detected.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  [1] Install Ollama locally (recommended)")
	fmt.Fprintln(out, "  [2] Use an existing Ollama server elsewhere on the network")
	fmt.Fprintln(out, "  [3] Skip AI setup for now")
	fmt.Fprintln(out)

	// If stdin is not a terminal (piped), print guidance and skip.
	if in == nil {
		fmt.Fprintln(out, "  To install Ollama, visit: https://ollama.com/download")
		return engineChoiceSkip, ""
	}

	scanner := bufio.NewScanner(in)
	fmt.Fprint(out, "  Choice [1]: ")
	if !scanner.Scan() {
		return engineChoiceInstallLocal, "" // default on EOF
	}
	switch strings.TrimSpace(scanner.Text()) {
	case "", "1":
		return engineChoiceInstallLocal, ""
	case "2":
		fmt.Fprint(out, "  Ollama server URL (e.g. http://mac-studio.local:11434): ")
		if !scanner.Scan() {
			return engineChoiceSkip, ""
		}
		return engineChoiceRemote, strings.TrimSpace(scanner.Text())
	case "3":
		return engineChoiceSkip, ""
	default:
		fmt.Fprintln(out, "  Unrecognized choice; skipping AI setup for now.")
		return engineChoiceSkip, ""
	}
}

// promptInstallLlamaServer displays the managed runtime install prompt.
func promptInstallLlamaServer(out io.Writer, in io.Reader, autoInstall bool) bool {
	if autoInstall {
		return true
	}
	fmt.Fprintln(out, "  Managed llama-server runtime is not installed.")
	fmt.Fprintln(out, "  Cercano can install llama.cpp and supervise llama-server as an isolated local runtime.")
	fmt.Fprintln(out)
	if in == nil {
		fmt.Fprintln(out, "  Install llama.cpp so `llama-server` is available, then re-run 'cercano setup'.")
		return false
	}
	fmt.Fprint(out, "  Install llama-server runtime now? [Y/n]: ")
	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		return parseYesNo(scanner.Text())
	}
	return true
}

// ollamaInstallCommand returns the command and args to install Ollama on the given platform.
// Returns empty command if installation cannot be automated.
func ollamaInstallCommand(goos string, hasHomebrew bool) (string, []string) {
	switch goos {
	case "darwin":
		if hasHomebrew {
			return "brew", []string{"install", "ollama"}
		}
		return "", nil
	case "linux":
		return "sh", []string{"-c", "curl -fsSL https://ollama.com/install.sh | sh"}
	default:
		return "", nil
	}
}

// llamaServerInstallCommand returns the command and args to install llama.cpp,
// which provides the llama-server binary. Homebrew supports macOS and Linux.
func llamaServerInstallCommand(goos string, hasHomebrew bool) (string, []string) {
	switch goos {
	case "darwin", "linux":
		if hasHomebrew {
			return "brew", []string{"install", "llama.cpp"}
		}
		return "", nil
	default:
		return "", nil
	}
}

// ollamaStartCommand returns the command and args to start Ollama on the given platform.
func ollamaStartCommand(goos string, hasHomebrew bool) (string, []string) {
	switch goos {
	case "darwin":
		if hasHomebrew {
			return "brew", []string{"services", "start", "ollama"}
		}
		return "ollama", []string{"serve"}
	case "linux":
		return "ollama", []string{"serve"}
	default:
		return "ollama", []string{"serve"}
	}
}

// hasBrewInstalled checks if Homebrew is available on the system.
func hasBrewInstalled() bool {
	_, err := exec.LookPath("brew")
	return err == nil
}

// installOllama runs the platform-appropriate install command.
func installOllama(goos string, hasBrew bool) error {
	cmd, args := ollamaInstallCommand(goos, hasBrew)
	if cmd == "" {
		return fmt.Errorf("automatic installation is not available on this platform. Please install Ollama manually from https://ollama.com/download")
	}

	fmt.Fprintf(os.Stderr, "  Installing Ollama")
	if goos == "darwin" && hasBrew {
		fmt.Fprintf(os.Stderr, " via Homebrew")
	}
	fmt.Fprintln(os.Stderr, "...")

	proc := exec.Command(cmd, args...)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if err := proc.Run(); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "  OK: Ollama installed.")
	return nil
}

// installLlamaServerRuntime installs llama.cpp, which provides llama-server.
func installLlamaServerRuntime(goos string, hasBrew bool) error {
	cmd, args := llamaServerInstallCommand(goos, hasBrew)
	if cmd == "" {
		return fmt.Errorf("automatic llama-server installation is not available on this platform. Install llama.cpp manually so `llama-server` is on PATH")
	}
	fmt.Fprintln(os.Stderr, "  Installing llama.cpp runtime...")
	proc := exec.Command(cmd, args...)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if err := proc.Run(); err != nil {
		return fmt.Errorf("llama.cpp installation failed: %w", err)
	}
	if _, err := exec.LookPath("llama-server"); err != nil {
		return fmt.Errorf("llama.cpp installed, but llama-server was not found on PATH")
	}
	fmt.Fprintln(os.Stderr, "  OK: llama-server runtime installed.")
	return nil
}

// startOllama attempts to start the Ollama service.
func startOllama(goos string, hasBrew bool) error {
	fmt.Fprintln(os.Stderr, "  Starting Ollama...")

	cmd, args := ollamaStartCommand(goos, hasBrew)
	proc := exec.Command(cmd, args...)

	// For "ollama serve" (non-brew), run in background
	if cmd == "ollama" {
		proc.Stdout = nil
		proc.Stderr = nil
		if err := proc.Start(); err != nil {
			return fmt.Errorf("failed to start Ollama: %w", err)
		}
		// Don't wait — it runs as a background process
		return nil
	}

	// For brew services, run and wait
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if err := proc.Run(); err != nil {
		// Fall back to direct start if brew services fails
		fallback := exec.Command("ollama", "serve")
		fallback.Stdout = nil
		fallback.Stderr = nil
		if fbErr := fallback.Start(); fbErr != nil {
			return fmt.Errorf("failed to start Ollama: %w (brew services also failed: %v)", fbErr, err)
		}
	}
	return nil
}

// waitForEngine polls the engine health check until it succeeds or maxAttempts is reached.
// Each attempt waits 1 second before retrying.
func waitForEngine(check engineCheckFunc, url string, maxAttempts int) error {
	for i := 0; i < maxAttempts; i++ {
		if err := check(url); err == nil {
			return nil
		}
		if i < maxAttempts-1 {
			time.Sleep(1 * time.Second)
		}
	}
	return fmt.Errorf("engine at %s did not become responsive after %d attempts", url, maxAttempts)
}
