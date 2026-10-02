// Package fixtures builds this spike's own test binaries and packages them
// into release assets with the EXACT current Cercano Windows naming and
// layout. Everything here is test scaffolding for the spike: no production
// code, no network, no credentials, no real Cercano.
package fixtures

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// agentPkg and cliPkg are the two fixture entrypoints mirroring the two real
// Cercano binaries shipped in the Windows ZIP.
const (
	agentPkg = "./cmd/fixture-agent"
	cliPkg   = "./cmd/fixture-cli"

	AgentName = "cercano.exe"
	CliName   = "cercano-cli.exe"
)

// ModuleRoot returns the directory holding this spike's go.mod.
func ModuleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// buildFixture compiles a fixture binary from this spike's own source.
// goos/goarch default to the host; pass "windows"/"amd64" to cross-compile.
func buildFixture(t *testing.T, goos, goarch, pkg, outName, version string, unhealthy bool) string {
	t.Helper()
	root := ModuleRoot(t)
	out := filepath.Join(t.TempDir(), outName)

	ld := fmt.Sprintf("-X main.version=%s", version)
	if unhealthy {
		ld += " -X main.unhealthy=true"
	}

	cmd := exec.Command("go", "build", "-o", out, "-ldflags", ld, pkg)
	cmd.Dir = root
	env := append(os.Environ(), "CGO_ENABLED=0")
	if goos != "" {
		env = append(env, "GOOS="+goos)
	}
	if goarch != "" {
		env = append(env, "GOARCH="+goarch)
	}
	cmd.Env = env
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, outb)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", out, err)
	}
	return out
}

// BuildAgent builds the cercano.exe stand-in for the HOST platform so tests
// can execute it and observe real OS behavior.
func BuildAgent(t *testing.T, version string, unhealthy bool) string {
	t.Helper()
	return buildFixture(t, runtime.GOOS, runtime.GOARCH, agentPkg, AgentName, version, unhealthy)
}

// BuildCli builds the cercano-cli.exe stand-in for the HOST platform.
func BuildCli(t *testing.T, version string) string {
	t.Helper()
	return buildFixture(t, runtime.GOOS, runtime.GOARCH, cliPkg, CliName, version, false)
}

// BuildAgentFor compiles the cercano.exe stand-in for an explicit platform
// (e.g. GOOS=windows) WITHOUT executing it; used for cross-compile checks.
func BuildAgentFor(t *testing.T, goos, goarch, version string) string {
	t.Helper()
	return buildFixture(t, goos, goarch, agentPkg, AgentName, version, false)
}

// RunVersion executes "<exe> --version" and returns trimmed stdout.
func RunVersion(t *testing.T, exe string) string {
	t.Helper()
	out, err := exec.Command(exe, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", exe, err, out)
	}
	return strings.TrimSpace(string(out))
}

// RunHealth executes "<exe> --health" and returns nil when it exits 0.
func RunHealth(exe string) error {
	return exec.Command(exe, "--health").Run()
}

// AssetName returns the exact production archive name for a version:
// cercano-<version>-windows-x64.zip
func AssetName(version string) string {
	return fmt.Sprintf("cercano-%s-windows-x64.zip", version)
}

// AssetRoot returns the single nested root directory inside the archive:
// cercano-<version>-windows-x64
func AssetRoot(version string) string {
	return fmt.Sprintf("cercano-%s-windows-x64", version)
}

// MakeCercanoRelease packages agentPath and cliPath into
// cercano-<version>-windows-x64.zip plus the production-format
// .sha256 sidecar ("<hex>  <basename>\n") inside dir.
//
// The archive layout mirrors scripts/build-windows-release.py exactly:
//
//	cercano-<version>-windows-x64/
//	├── bin/
//	│   ├── cercano.exe
//	│   └── cercano-cli.exe
//	├── LICENSE
//	└── README.txt
//
// agentPath/cliPath are fixture binaries (host-built by BuildAgent/BuildCli
// in portable tests, so tests can run them; the real artifact contains
// windows/amd64 PE binaries). An empty cliPath produces an archive WITHOUT
// cercano-cli.exe (for incomplete-archive experiments).
func MakeCercanoRelease(t *testing.T, dir, version, agentPath, cliPath string) (zipPath, shaPath string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	name := AssetName(version)
	root := AssetRoot(version)
	zipPath = filepath.Join(dir, name)

	entries := []struct {
		arcname string
		source  string
		mode    os.FileMode
	}{
		{root + "/LICENSE", "", 0o644},
		{root + "/README.txt", "", 0o644},
		{root + "/bin/" + AgentName, agentPath, 0o755},
	}
	// An empty cliPath omits cercano-cli.exe — used by experiments that must
	// reject incomplete archives.
	if cliPath != "" {
		entries = append(entries, struct {
			arcname string
			source  string
			mode    os.FileMode
		}{root + "/bin/" + CliName, cliPath, 0o755})
	}

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create %s: %v", zipPath, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return data
	}
	for _, e := range entries {
		var data []byte
		if e.source == "" {
			data = []byte(fmt.Sprintf("SP spike fixture placeholder for %s\n", e.arcname))
		} else {
			data = read(e.source)
		}
		hdr := &zip.FileHeader{Name: e.arcname, Method: zip.Deflate}
		// create_system=3 (unix) so stored modes are honored, exactly like
		// the production builder.
		hdr.SetMode(e.mode)
		hdr.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip header %s: %v", e.arcname, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", e.arcname, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	digest := FileSHA256(t, zipPath)
	shaPath = zipPath + ".sha256"
	// Production sidecar format: "<digest>  <basename>\n" (two spaces), as
	// written by scripts/build-windows-release.py.
	if err := os.WriteFile(shaPath, []byte(fmt.Sprintf("%s  %s\n", digest, name)), 0o644); err != nil {
		t.Fatalf("write %s: %v", shaPath, err)
	}
	return zipPath, shaPath
}

// MakeCercanoReleaseBytes is MakeCercanoRelease returning raw archive bytes
// (for tampering experiments) plus the sidecar path.
func MakeCercanoReleaseBytes(t *testing.T, dir, version, agentPath, cliPath string) (zipBytes []byte, shaPath string) {
	t.Helper()
	zipPath, shaPath := MakeCercanoRelease(t, dir, version, agentPath, cliPath)
	zipBytes = readFile(t, zipPath)
	return zipBytes, shaPath
}

// FileSHA256 returns the hex sha256 of a file.
func FileSHA256(t *testing.T, path string) string {
	t.Helper()
	data := readFile(t, path)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// readFile is os.ReadFile with test-fatal error handling.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
