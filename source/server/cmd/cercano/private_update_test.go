package main

// Tests for the private updater dispatch (private_update.go) and the
// updater-protocol capability query. Two layers:
//
//  1. In-process unit tests over dispatchPrivateUpdate with an injected
//     sentinel runner that fails the test if a rejected invocation ever
//     reaches it.
//  2. Real-subprocess fixtures: the test binary re-executes itself with an
//     isolated HOME/XDG/APPDATA and an env-provided argv, calls the REAL
//     main(), and proves the private paths exit with the documented codes
//     without creating any files or doing any normal main initialization.
//     Only the private/capability/version fixture arguments are ever used —
//     no test runs the normal agent mode.
//
// The subprocesses are bounded by a context deadline and reaped via
// exec.CommandContext + Wait. No network, models, secrets, state, or real
// home directory are touched.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/oneshot"
	"cercano/source/server/internal/updatecoord/operation"
)

// fixtureEnvVar carries the argv fixture for the re-executed test binary's
// TestMain helper. Its presence is the only trigger for helper mode.
const fixtureEnvVar = "CERCANO_TEST_MAIN_FIXTURE_ARGS"

// TestMain either runs the package tests or, when the fixture env var is
// set, installs the fixture argv and calls the REAL main(). Helper mode is
// reached only from runMainFixture, which passes exclusively private
// dispatch / capability / version arguments, so the agent server mode can
// never start from these tests.
func TestMain(m *testing.M) {
	if raw := os.Getenv(fixtureEnvVar); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			os.Exit(97)
		}
		os.Args = append([]string{"cercano"}, args...)
		main()
		// main() returned rather than exiting: treat as exit 0 (the plain
		// version path does this).
		return
	}
	os.Exit(m.Run())
}

// sentinelRunner fails the test if it is ever called.
func sentinelRunner(t *testing.T) privateUpdateRunner {
	t.Helper()
	return func(ctx context.Context, req oneshot.Request) (oneshot.Result, error) {
		t.Errorf("runner callback reached with rejected inputs: %+v", req)
		return oneshot.Result{}, errors.New("must not be called")
	}
}

func TestDispatchPrivateUpdateNotClaimedForNormalArguments(t *testing.T) {
	for _, args := range [][]string{
		{"version"},
		{"agent"},
		{"stats"},
		{"--mcp"},
	} {
		var out, errOut strings.Builder
		code, claimed := dispatchPrivateUpdate(args, &out, &errOut, sentinelRunner(t))
		if claimed {
			t.Errorf("args %v: unexpectedly claimed (code %d)", args, code)
		}
		if code != 0 {
			t.Errorf("args %v: non-zero code %d for unclaimed invocation", args, code)
		}
	}
}

func TestDispatchPrivateUpdateRejectsMalformedBeforeCallback(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown private prefix":       {"__internal-updat", "--install-id", "abc", "--operation-id", "1"},
		"other reserved token":         {"__maintenance"},
		"no values":                    {"__internal-update", "--install-id"},
		"wrong order":                  {"__internal-update", "--operation-id", "1", "--install-id", "abc"},
		"duplicate install flag":       {"__internal-update", "--install-id", "abc", "--install-id", "abc", "--operation-id", "1"},
		"duplicate operation flag":     {"__internal-update", "--install-id", "abc", "--operation-id", "1", "--operation-id", "1"},
		"missing operation value":      {"__internal-update", "--install-id", "abc", "--operation-id"},
		"trailing command tokens":      {"__internal-update", "--install-id", "abc", "--operation-id", "1", "sh", "-c", "echo feed"},
		"feed input in value position": {"__internal-update", "--install-id", "abc", "--operation-id", "1", "--"},
		"empty install id":             {"__internal-update", "--install-id", "", "--operation-id", "1"},
		"path install id":              {"__internal-update", "--install-id", "../escape", "--operation-id", "1"},
		"absolute path install id":     {"__internal-update", "--install-id", "/tmp/x", "--operation-id", "1"},
		"uppercase install id":         {"__internal-update", "--install-id", "Abc", "--operation-id", "1"},
		"dos device install id":        {"__internal-update", "--install-id", "con", "--operation-id", "1"},
		"dot install id":               {"__internal-update", "--install-id", ".", "--operation-id", "1"},
		"operation id zero":            {"__internal-update", "--install-id", "abc", "--operation-id", "0"},
		"operation id negative":        {"__internal-update", "--install-id", "abc", "--operation-id", "-1"},
		"operation id plus sign":       {"__internal-update", "--install-id", "abc", "--operation-id", "+1"},
		"operation id leading zero":    {"__internal-update", "--install-id", "abc", "--operation-id", "01"},
		"operation id float":           {"__internal-update", "--install-id", "abc", "--operation-id", "1.5"},
		"operation id spaces":          {"__internal-update", "--install-id", "abc", "--operation-id", " 1"},
		"operation id non-numeric":     {"__internal-update", "--install-id", "abc", "--operation-id", "one"},
		"operation id int64 overflow":  {"__internal-update", "--install-id", "abc", "--operation-id", "9223372036854775808"},
		"operation id empty":           {"__internal-update", "--install-id", "abc", "--operation-id", ""},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errOut strings.Builder
			code, claimed := dispatchPrivateUpdate(args, &out, &errOut, sentinelRunner(t))
			if !claimed {
				t.Fatalf("args %v: reserved invocation not claimed", args)
			}
			if code != privateExitUsage {
				t.Fatalf("args %v: code = %d, want %d (usage)", args, code, privateExitUsage)
			}
			if errOut.Len() == 0 {
				t.Errorf("args %v: no diagnostic on errOut", args)
			}
			if out.Len() != 0 {
				t.Errorf("args %v: wrote to stdout on a rejection: %q", args, out.String())
			}
		})
	}
}

func TestDispatchPrivateUpdateValidInputsCallInjectedRunner(t *testing.T) {
	var got []oneshot.Request
	runner := func(ctx context.Context, req oneshot.Request) (oneshot.Result, error) {
		got = append(got, req)
		return oneshot.Result{OperationID: req.OperationID, State: operation.StateComplete, TargetVersion: "9.9"}, nil
	}
	var out, errOut strings.Builder
	code, claimed := dispatchPrivateUpdate(
		[]string{"__internal-update", "--install-id", "abc", "--operation-id", "42"},
		&out, &errOut, runner)
	if !claimed {
		t.Fatal("valid invocation not claimed")
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if len(got) != 1 {
		t.Fatalf("runner called %d times, want 1", len(got))
	}
	if got[0].InstallID != "abc" || got[0].OperationID != 42 {
		t.Fatalf("runner got %+v, want install abc / operation 42", got[0])
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr on success: %q", errOut.String())
	}
}

func TestDispatchPrivateUpdateRunnerFailureExitsNonzero(t *testing.T) {
	runner := func(ctx context.Context, req oneshot.Request) (oneshot.Result, error) {
		return oneshot.Result{}, errors.New("backend refused")
	}
	var out, errOut strings.Builder
	code, claimed := dispatchPrivateUpdate(
		[]string{"__internal-update", "--install-id", "abc", "--operation-id", "7"},
		&out, &errOut, runner)
	if !claimed {
		t.Fatal("valid invocation not claimed")
	}
	if code != privateExitExecution {
		t.Fatalf("code = %d, want %d (execution failure)", code, privateExitExecution)
	}
}

func TestDispatchPrivateUpdateProductionRunnerNilIsExplicitlyUnavailable(t *testing.T) {
	var out, errOut strings.Builder
	code, claimed := dispatchPrivateUpdate(
		[]string{"__internal-update", "--install-id", "abc", "--operation-id", "9"},
		&out, &errOut, nil)
	if !claimed {
		t.Fatal("valid invocation not claimed")
	}
	if code != privateExitUnavailable {
		t.Fatalf("code = %d, want %d (unavailable)", code, privateExitUnavailable)
	}
	if !strings.Contains(errOut.String(), "unavailable") {
		t.Fatalf("stderr = %q, want explicit unavailability diagnostic", errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestUpdaterProtocolCapabilityJSON(t *testing.T) {
	var sb strings.Builder
	if err := printUpdaterProtocol(&sb); err != nil {
		t.Fatalf("printUpdaterProtocol: %v", err)
	}
	var info updaterProtocolInfo
	if err := json.Unmarshal([]byte(sb.String()), &info); err != nil {
		t.Fatalf("capability output is not JSON: %v (raw %q)", err, sb.String())
	}
	if info.Component != "cercano" {
		t.Errorf("component = %q, want cercano", info.Component)
	}
	if info.Protocol != 1 {
		t.Errorf("protocol = %d, want 1", info.Protocol)
	}
	if info.ExecutionReady {
		t.Errorf("execution_ready = true, want false until execution is wired")
	}
}

// runMainFixture re-executes this test binary in helper mode: the REAL main()
// runs with the given argv under an isolated HOME / XDG_CONFIG_HOME /
// XDG_DATA_HOME / XDG_CACHE_HOME / APPDATA. The subprocess is bounded by a
// context deadline (and killed on it) and reaped via Wait. It returns the
// process's stdout, stderr, and exit code, and FAILS the test if the
// isolated directories were written to (proving no config, DB, cache, or
// any other file was created and hence no normal main initialization ran).
func runMainFixture(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	roots := map[string]string{}
	env := []string{}
	for _, kv := range os.Environ() {
		// Strip the environment we are about to isolate.
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		switch strings.ToUpper(key) {
		case "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
			"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME":
		default:
			env = append(env, kv)
		}
	}
	home := t.TempDir()
	roots["HOME"] = home
	env = append(env, "HOME="+home, "USERPROFILE="+home)
	for _, kv := range []struct{ key, sub string }{
		{"APPDATA", "appdata"},
		{"LOCALAPPDATA", "localappdata"},
		{"XDG_STATE_HOME", "xdg-state"},
		{"XDG_CONFIG_HOME", "xdg-config"},
		{"XDG_DATA_HOME", "xdg-data"},
		{"XDG_CACHE_HOME", "xdg-cache"},
	} {
		dir := filepath.Join(home, kv.sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("isolating %s: %v", kv.key, err)
		}
		roots[kv.key] = dir
		env = append(env, kv.key+"="+dir)
	}

	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal fixture args: %v", err)
	}
	env = append(env, fixtureEnvVar+"="+string(rawArgs))

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolving test binary: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run() // Wait reaps the process; the deadline kill bounds it.
	if ctx.Err() != nil {
		t.Fatalf("fixture subprocess for %v was not reaped in time (deadline)", args)
	}
	code := 0
	var exitErr *exec.ExitError
	if runErr != nil && errors.As(runErr, &exitErr) {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		t.Fatalf("running fixture %v: %v", args, runErr)
	}

	// No normal main initialization: nothing may be created under any of the
	// isolated roots.
	for key, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("reading isolated %s: %v", key, err)
		}
		// HOME contains the subdirectories we created; only those may exist.
		for _, e := range entries {
			if key == "HOME" && (e.Name() == "appdata" || e.Name() == "localappdata" || e.Name() == "xdg-state" || e.Name() == "xdg-config" || e.Name() == "xdg-data" || e.Name() == "xdg-cache") {
				continue
			}
			t.Errorf("normal main initialization touched isolated %s: created %s", key, e.Name())
		}
	}
	return stdout.String(), stderr.String(), code
}

func TestMainSubprocessInvalidPrivateInvocation(t *testing.T) {
	stdout, stderr, code := runMainFixture(t,
		"__internal-update", "--install-id", "../escape", "--operation-id", "1")
	if code != privateExitUsage {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, privateExitUsage, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "install id") && !strings.Contains(stderr, "malformed") {
		t.Fatalf("stderr = %q, want a rejection diagnostic", stderr)
	}
}

func TestMainSubprocessUnknownPrivatePrefixRejected(t *testing.T) {
	_, stderr, code := runMainFixture(t, "__nosuch-command")
	if code != privateExitUsage {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, privateExitUsage, stderr)
	}
	if !strings.Contains(stderr, "unknown private command") {
		t.Fatalf("stderr = %q, want unknown-private-command diagnostic", stderr)
	}
}

func TestMainSubprocessValidPrivateUnavailable(t *testing.T) {
	stdout, stderr, code := runMainFixture(t,
		"__internal-update", "--install-id", "abc", "--operation-id", "7")
	if code != privateExitUnavailable {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, privateExitUnavailable, stderr)
	}
	if !strings.Contains(stderr, "unavailable") {
		t.Fatalf("stderr = %q, want explicit unavailability diagnostic", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestMainSubprocessCapabilityQuery(t *testing.T) {
	stdout, stderr, code := runMainFixture(t, "version", "--updater-protocol")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	var info updaterProtocolInfo
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatalf("capability stdout is not JSON: %v (raw %q)", err, stdout)
	}
	if info.Component != "cercano" || info.Protocol != 1 || info.ExecutionReady {
		t.Fatalf("capability = %+v, want component cercano / protocol 1 / execution_ready false", info)
	}
}

func TestMainSubprocessPlainVersionUnchanged(t *testing.T) {
	stdout, stderr, code := runMainFixture(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if stdout != "cercano vdev\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "cercano vdev\n")
	}
}
