package agentclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBinaryDiscoveryInstalledLayout proves sibling binary discovery
// (findCercanoBinary) against an installed, Homebrew-style layout:
//
//   - Cellar keg holds the real binaries; a prefix bin/ directory holds
//     brew-link-style symlinks to the keg, mirroring `brew install cercano`.
//   - The CLI copy is executed as a real subprocess, launched from a
//     working directory outside the repository that also contains a decoy
//     agent binary, proving discovery is based on the executable location,
//     never the working directory.
//   - Both invocation directions are covered: through the prefix symlink
//     (how a user runs `cercano-cli` after brew install) and via the real
//     Cellar path (what a fully resolved executable path yields).
//   - With a hostile `cercano` sitting first in PATH, the colocated sibling
//     must win and the hostile binary must never be executed.
//
// Isolation: the copied test binary only runs the helper below, which calls
// findCercanoBinary — a stat/LookPath lookup only. Agent fixtures are never
// executed, so no agent is launched and no config, socket, or port is
// touched. PATH and HOME are filtered and pointed at empty temp
// directories so a developer installation can never leak into the probe.

func TestBinaryDiscoveryInstalledLayout(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	modes := []struct {
		name        string
		invocation  string // "prefix_symlink" or "cellar_direct"
		hostilePATH bool
	}{
		{"prefix_symlink_invocation", "prefix_symlink", false},
		{"cellar_direct_invocation", "cellar_direct", false},
		{"sibling_preferred_over_hostile_path", "prefix_symlink", true},
	}
	// One shared installed layout (exactly like one `brew install cercano`
	// keg): both invocation directions must resolve to the same keg agent.
	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar", "cercano", "1.0.0", "bin")
	prefix := filepath.Join(root, "bin")
	outside := filepath.Join(root, "outside")
	home := filepath.Join(root, "home")
	empty := filepath.Join(root, "empty")
	hostileDir := filepath.Join(root, "hostile")
	for _, dir := range []string{cellar, prefix, outside, home, empty, hostileDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Real CLI copy in the keg, exactly like a bottle install.
	installed := filepath.Join(cellar, "cercano-cli")
	copyDiscoveryExecutable(t, exe, installed)
	// Agent fixture in the keg: discovery only stats it, never runs it.
	agent := filepath.Join(cellar, cercanoBinaryName)
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// Homebrew-style prefix symlinks (what `brew link` creates).
	for _, name := range []string{"cercano-cli", cercanoBinaryName} {
		if err := os.Symlink(filepath.Join(cellar, name), filepath.Join(prefix, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Decoy agent in the isolated working directory: a cwd-based (buggy)
	// lookup would find this instead of the keg sibling.
	if err := os.WriteFile(filepath.Join(outside, cercanoBinaryName), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// A hostile agent first in PATH. It would leave a marker file if
	// anything ever executed it; discovery must not even pick it.
	hostileAgent := filepath.Join(hostileDir, cercanoBinaryName)
	hostileMarker := filepath.Join(root, "hostile-executed")
	hostileScript := "#!/bin/sh\necho executed >> " + hostileMarker + "\nexit 42\n"
	if err := os.WriteFile(hostileAgent, []byte(hostileScript), 0755); err != nil {
		t.Fatal(err)
	}
	foundByMode := make(map[string]string)
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			command := filepath.Join(prefix, "cercano-cli")
			if mode.invocation == "cellar_direct" {
				command = installed
			}
			searchPath := empty
			if mode.hostilePATH {
				searchPath = hostileDir
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, command, "-test.run=^TestBinaryDiscoveryInstalledHelper$", "-test.count=1")
			// Filter inherited values so duplicate environment keys cannot weaken
			// isolation of PATH/HOME.
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				if key != "PATH" && key != "HOME" && !strings.HasPrefix(key, "CERCANO_DISCOVERY_") {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env,
				"PATH="+searchPath,
				"HOME="+home,
				"CERCANO_DISCOVERY_INSTALLED_HELPER=1",
				"CERCANO_DISCOVERY_INSTALLED_INVOKED="+command,
				"CERCANO_DISCOVERY_INSTALLED_CWD="+outside,
			)
			cmd.Dir = outside
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helper subprocess: %v\n%s", err, output)
			}
			report := parseInstalledHelperReport(t, output)
			// The subprocess really ran the copied executable at the invoked
			// path — through the prefix symlink in the symlink modes.
			assertInstalledSameFile(t, report.exe, command, "subprocess executable")
			// The probe ran from the isolated working directory outside the
			// checkout (macOS may report a /private-prefixed path; compare inode).
			assertInstalledSameFile(t, report.wd, outside, "working directory")
			// Discovery resolved to the keg agent, not the cwd decoy or a PATH hit.
			assertInstalledSameFile(t, report.found, agent, "discovered binary")
			if mode.hostilePATH {
				if isInstalledSameFile(report.found, hostileAgent) {
					t.Fatalf("discovery returned the hostile PATH binary %q", report.found)
				}
				if _, err := os.Stat(hostileMarker); err == nil {
					t.Fatal("hostile PATH binary was executed")
				}
				if !isInstalledSameFile(report.found, agent) {
					t.Fatalf("discovery %q is not the colocated keg sibling", report.found)
				}
			}
			foundByMode[mode.name] = report.found
			t.Logf("invoked=%s exe=%q wd=%q found=%q", command, report.exe, report.wd, report.found)
		})
	}
	// Both invocation directions must converge on the same keg agent binary.
	assertInstalledSameFile(t,
		foundByMode["prefix_symlink_invocation"],
		foundByMode["cellar_direct_invocation"],
		"agent discovered from both invocation directions")
}

// TestBinaryDiscoveryInstalledHelper runs only inside the copied test
// subprocess spawned by TestBinaryDiscoveryInstalledLayout. It calls the real
// production lookup (findCercanoBinary) and reports os.Executable(), the
// working directory, and the discovery result on stdout for the parent to
// assert against the fixture layout.
func TestBinaryDiscoveryInstalledHelper(t *testing.T) {
	if os.Getenv("CERCANO_DISCOVERY_INSTALLED_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	found, err := findCercanoBinary()
	if err != nil {
		t.Fatalf("findCercanoBinary: %v", err)
	}
	fmt.Printf("exe: %s\nwd: %s\nfound: %s\n", exe, wd, found)
}

type installedHelperReport struct {
	exe   string
	wd    string
	found string
}

func parseInstalledHelperReport(t *testing.T, output []byte) installedHelperReport {
	t.Helper()
	report := installedHelperReport{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			continue
		}
		switch key {
		case "exe":
			report.exe = value
		case "wd":
			report.wd = value
		case "found":
			report.found = value
		}
	}
	if report.exe == "" || report.wd == "" || report.found == "" {
		t.Fatalf("incomplete helper report (exe=%q wd=%q found=%q)\n%s", report.exe, report.wd, report.found, output)
	}
	return report
}

func assertInstalledSameFile(t *testing.T, actual, expected, what string) {
	t.Helper()
	actualInfo, err := os.Stat(actual)
	if err != nil {
		t.Fatalf("%s %q: %v", what, actual, err)
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("%s expected %q: %v", what, expected, err)
	}
	if !os.SameFile(actualInfo, expectedInfo) {
		t.Fatalf("%s: %q does not resolve to %q", what, actual, expected)
	}
}

func isInstalledSameFile(a, b string) bool {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(aInfo, bInfo)
}
