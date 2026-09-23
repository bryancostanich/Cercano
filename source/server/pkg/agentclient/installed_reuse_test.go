package agentclient

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

// TestInstalledAgentReuse proves the upgrade-safety behavior the release
// depends on: when an agent is already listening, a newly installed terminal
// client attaches to it and never spawns a second agent process.
//
// This is the non-disruptive upgrade contract from the effort spec. After
// `brew upgrade`, the replaced client binary must join the running agent
// rather than starting a competing one, and no version gate may stand in the
// way (none exists in ensureServerLaunched / connect, and none is added here).
//
// Both directions are asserted so neither is vacuous:
//
//   - running_agent_is_reused: a real gRPC listener stands in for the running
//     agent. ensureServerLaunched must report that it launched nothing, and
//     the agent fixture on disk must never execute.
//   - absent_agent_is_launched: with nothing listening, the same fixture must
//     execute. This is the control proving the marker file actually fires,
//     so a green reuse assertion cannot come from a broken fixture.
//
// Isolation: the probe runs in a subprocess whose TMPDIR points at a private
// directory, so the auto-launch lock and the server log are isolated from the
// developer's live agent. PATH and HOME are filtered and emptied, the
// listener binds 127.0.0.1 on an ephemeral port, and the only executable that
// can ever run is the throwaway shell fixture. No real agent is started,
// signaled, or connected to.
func TestInstalledAgentReuse(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix installation layouts")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"running_agent_is_reused", "absent_agent_is_launched"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cellar := filepath.Join(root, "Cellar", "cercano", "1.0.0", "bin")
			prefix := filepath.Join(root, "bin")
			outside := filepath.Join(root, "outside")
			home := filepath.Join(root, "home")
			empty := filepath.Join(root, "empty")
			isolatedTmp := filepath.Join(root, "tmp")
			for _, dir := range []string{cellar, prefix, outside, home, empty, isolatedTmp} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			// The installed client: a real copy of this test executable,
			// reached through a Homebrew-style prefix symlink.
			copyDiscoveryExecutable(t, exe, filepath.Join(cellar, "cercano-cli"))
			// The installed agent: a fixture that records its own execution.
			// It never listens, so launching it can never be mistaken for reuse.
			marker := filepath.Join(root, "agent-spawned")
			fixture := "#!/bin/sh\necho spawned >> " + marker + "\nexit 0\n"
			if err := os.WriteFile(filepath.Join(cellar, cercanoBinaryName), []byte(fixture), 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"cercano-cli", cercanoBinaryName} {
				if err := os.Symlink(filepath.Join(cellar, name), filepath.Join(prefix, name)); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			command := filepath.Join(prefix, "cercano-cli")
			cmd := exec.CommandContext(ctx, command, "-test.run=^TestInstalledAgentReuseHelper$", "-test.count=1", "-test.v")
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				if key != "PATH" && key != "HOME" && key != "TMPDIR" && !strings.HasPrefix(key, "CERCANO_REUSE_") {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env,
				"PATH="+empty,
				"HOME="+home,
				// Isolates the auto-launch lock and server log from the live agent.
				"TMPDIR="+isolatedTmp,
				"CERCANO_REUSE_HELPER=1",
				"CERCANO_REUSE_MODE="+mode,
			)
			cmd.Dir = outside
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helper subprocess: %v\n%s", err, output)
			}
			report := parseInstalledHelperFields(t, output, "launched", "logpath", "error")

			switch mode {
			case "running_agent_is_reused":
				if report["launched"] != "false" {
					t.Fatalf("a second agent was launched while one was running (launched=%q)\n%s", report["launched"], output)
				}
				if report["error"] != "<nil>" {
					t.Fatalf("attaching to the running agent failed: %s\n%s", report["error"], output)
				}
				if report["logpath"] != "" {
					t.Fatalf("reuse produced a spawn log %q, implying a launch\n%s", report["logpath"], output)
				}
				if _, err := os.Stat(marker); err == nil {
					t.Fatalf("the installed agent binary was executed despite a running agent\n%s", output)
				}
			case "absent_agent_is_launched":
				// Control: proves the marker fires, so the reuse assertion above
				// cannot pass merely because the fixture never runs.
				if report["launched"] != "true" {
					t.Fatalf("no agent was launched when none was running (launched=%q)\n%s", report["launched"], output)
				}
				deadline := time.Now().Add(10 * time.Second)
				for {
					if _, err := os.Stat(marker); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the installed agent binary was never executed\n%s", output)
					}
					time.Sleep(50 * time.Millisecond)
				}
			}
			t.Logf("mode=%s launched=%s logpath=%q error=%s",
				mode, report["launched"], report["logpath"], report["error"])
		})
	}
}

// TestInstalledAgentReuseHelper runs only inside the copied client subprocess.
// It exercises the production auto-launch decision (ensureServerLaunched)
// against either a live listener or nothing at all, and reports the outcome.
func TestInstalledAgentReuseHelper(t *testing.T) {
	if os.Getenv("CERCANO_REUSE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	// Ephemeral loopback port: never the developer's configured agent address.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	timeout := 8 * time.Second

	if os.Getenv("CERCANO_REUSE_MODE") == "running_agent_is_reused" {
		// A real gRPC server stands in for the running agent, so the
		// production dial completes its HTTP/2 handshake exactly as it
		// would against a live agent. No agent services are registered:
		// ensureServerLaunched only needs the connection to come up.
		server := grpc.NewServer()
		go func() { _ = server.Serve(listener) }()
		defer server.Stop()
	} else {
		// Nothing may listen here, so the production code must launch.
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		// Keep the control fast: the fixture never binds the port, so the
		// launch path is expected to give up waiting for it.
		timeout = 2 * time.Second
	}

	logPath, launched, err := ensureServerLaunched(context.Background(), addr, timeout)
	fmt.Printf("launched: %v\nlogpath: %s\nerror: %v\n", launched, logPath, err)
}

// parseInstalledHelperFields reads "key: value" lines from helper output,
// requiring every requested key to be present so a silent helper cannot be
// mistaken for a passing assertion.
func parseInstalledHelperFields(t *testing.T, output []byte, keys ...string) map[string]string {
	t.Helper()
	fields := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		for _, want := range keys {
			if key == want {
				fields[want] = strings.TrimSpace(value)
				seen[want] = true
			}
		}
	}
	for _, want := range keys {
		if !seen[want] {
			t.Fatalf("helper did not report %q\n%s", want, output)
		}
	}
	return fields
}
