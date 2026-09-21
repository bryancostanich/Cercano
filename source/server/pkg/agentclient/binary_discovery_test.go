package agentclient

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Exercise the real os.Executable lookup from an installed test executable,
// without starting an agent or inheriting a developer installation from PATH.
func TestBinaryDiscovery(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix installation layouts")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"homebrew_symlinks", "path_fallback", "missing"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cellar := filepath.Join(root, "Cellar", "cercano", "1.0.0", "bin")
			prefix := filepath.Join(root, "bin")
			outside := filepath.Join(root, "outside")
			empty := filepath.Join(root, "empty")
			for _, dir := range []string{cellar, prefix, outside, empty} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			installed := filepath.Join(cellar, "cercano-cli")
			copyDiscoveryExecutable(t, exe, installed)
			command, searchPath, expected := installed, empty, ""
			switch mode {
			case "homebrew_symlinks":
				expected = filepath.Join(cellar, cercanoBinaryName)
				// Discovery only stats this executable fixture; it is never run.
				if err := os.WriteFile(expected, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"cercano-cli", cercanoBinaryName} {
					if err := os.Symlink(filepath.Join(cellar, name), filepath.Join(prefix, name)); err != nil {
						t.Fatal(err)
					}
				}
				command = filepath.Join(prefix, "cercano-cli")
			case "path_fallback":
				expected = filepath.Join(prefix, cercanoBinaryName)
				if err := os.WriteFile(expected, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
					t.Fatal(err)
				}
				searchPath = prefix
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, command, "-test.run=^TestBinaryDiscoveryHelper$", "-test.count=1")
			// Filter inherited values so duplicate environment keys cannot weaken isolation.
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				if key != "PATH" && !strings.HasPrefix(key, "CERCANO_DISCOVERY_") {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env, "PATH="+searchPath, "CERCANO_DISCOVERY_HELPER=1", "CERCANO_DISCOVERY_EXPECTED="+expected)
			cmd.Dir = outside
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("helper: %v\n%s", err, output)
			}
		})
	}
}

func TestBinaryDiscoveryHelper(t *testing.T) {
	if os.Getenv("CERCANO_DISCOVERY_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	expected := os.Getenv("CERCANO_DISCOVERY_EXPECTED")
	found, err := findCercanoBinary()
	if expected == "" {
		if err == nil || !strings.Contains(err.Error(), "looked next to cercano-cli and in $PATH") {
			t.Fatalf("missing binary: found %q, error %v", found, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	actualInfo, err := os.Stat(found)
	if err != nil {
		t.Fatal(err)
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(actualInfo, expectedInfo) {
		t.Fatalf("found %q, expected file %q", found, expected)
	}
}

func copyDiscoveryExecutable(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}
