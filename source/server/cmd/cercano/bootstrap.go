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

// promptInstallEngine displays the engine-agnostic install prompt and returns
// whether the user wants to proceed with installation.
// If autoInstall is true, skips the prompt and returns true.
func promptInstallEngine(out io.Writer, in io.Reader, autoInstall bool) bool {
	if autoInstall {
		return true
	}

	fmt.Fprintln(out, "  No AI engine backend was detected.")
	fmt.Fprintln(out, "  Would you like help installing one?")
	fmt.Fprintln(out, "  Ollama is recommended as the simplest path.")
	fmt.Fprintln(out)

	// If stdin is not a terminal (piped), print guidance and return false
	if in == nil {
		fmt.Fprintln(out, "  To install Ollama, visit: https://ollama.com/download")
		return false
	}

	fmt.Fprint(out, "  Install Ollama now? [Y/n]: ")
	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		return parseYesNo(scanner.Text())
	}
	return true // default yes on EOF
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
