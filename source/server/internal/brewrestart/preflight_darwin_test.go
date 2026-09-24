//go:build darwin && cgo

package brewrestart

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRestartPreflightRefusesUnsafeLaunch(t *testing.T) {
	for _, mode := range []string{"valid", "worker", "relative temp", "missing cwd", "non-executable", "symlink log", "FIFO log"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(root, "Cellar", "cercano", "2", "bin", "cercano")
			if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(executable, []byte("fixture; never executed"), 0700); err != nil {
				t.Fatal(err)
			}
			state := LaunchState{Args: []string{"cercano", "agent"}, Env: []string{"TMPDIR=" + root}, Directory: root}
			log := filepath.Join(root, "cercano-server.log")
			switch mode {
			case "worker":
				state.Args[1] = "worker"
			case "relative temp":
				state.Env = []string{"TMPDIR=relative"}
			case "missing cwd":
				state.Directory = filepath.Join(root, "missing")
			case "non-executable":
				if err := os.Chmod(executable, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink log":
				if err := os.Symlink(executable, log); err != nil {
					t.Fatal(err)
				}
			case "FIFO log":
				if err := syscall.Mkfifo(log, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = preflightRestart(executable, state)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("unexpected preflight result: %v", err)
			}
		})
	}
}

func TestRestartInstalledRequiresDeadlineBeforeInspection(t *testing.T) {
	if _, err := RestartInstalled(context.Background(), "/unused", netip.MustParseAddrPort("127.0.0.1:1")); err == nil {
		t.Fatal("unbounded restart allowed")
	}
}
