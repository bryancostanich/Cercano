package lib

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// FACT under test: stock DecompressCommand finds each Cercano binary inside
// the archive's NESTED ROOT layout (cercano-<v>-windows-x64/bin/*.exe) by
// matching the member's base filename. Each call extracts exactly ONE
// binary; there is no stock multi-file extraction.
func TestDecompressExtractsEachExeFromNestedRootLayout(t *testing.T) {
	zipPath, _, agentPath, cliPath := buildCercanoRelease(t)
	data := mustRead(t, zipPath)

	agentBytes := extractOne(t, data, fixtures.AgentName)
	if want := mustRead(t, agentPath); !bytes.Equal(agentBytes, want) {
		t.Fatal("extracted agent bytes differ from the packaged fixture agent")
	}

	cliBytes := extractOne(t, data, fixtures.CliName)
	if want := mustRead(t, cliPath); !bytes.Equal(cliBytes, want) {
		t.Fatal("extracted cli bytes differ from the packaged fixture cli")
	}

	// The agent extraction must not accidentally return the cli binary and
	// vice versa (name matching disambiguates cercano vs cercano-cli).
	if bytes.Equal(agentBytes, cliBytes) {
		t.Fatal("agent and cli extractions returned identical bytes")
	}
}

// FACT under test: when the archive does not contain the requested command,
// extraction fails with ErrExecutableNotFoundInArchive (here: an archive
// missing cercano-cli.exe).
func TestDecompressFailsWhenCommandMissingFromArchive(t *testing.T) {
	agent := fixtures.BuildAgent(t, nextVersion, false)
	dir := t.TempDir()
	// A single-member archive in the same nested-root layout.
	zp := filepath.Join(dir, fixtures.AssetName(nextVersion))
	writeSingleExeZip(t, zp, fixtures.AssetRoot(nextVersion), fixtures.AgentName, mustRead(t, agent))

	data := mustRead(t, zp)
	if _, err := selfupdate.DecompressCommand(
		bytes.NewReader(data), fixtures.AssetName(nextVersion), fixtures.CliName, "windows", "x64"); err == nil {
		t.Fatal("expected ErrExecutableNotFoundInArchive for missing cercano-cli.exe")
	}
}

// FACT under test (memory/DoS): the stock zip path buffers the ENTIRE archive
// in memory per extraction call (io.ReadAll of the source, then again of
// the zip), and enforces no size limit. A two-binary update therefore reads
// the full archive at least twice. Production use must wrap the source with
// a size-limiting reader.
func TestStockZipPathBuffersWholeArchivePerCallAndHasNoSizeLimit(t *testing.T) {
	zipPath, _, _, _ := buildCercanoRelease(t)
	data := mustRead(t, zipPath)

	counting := &countingReader{r: bytes.NewReader(data)}
	if _, err := selfupdate.DecompressCommand(counting, fixtures.AssetName(nextVersion), fixtures.AgentName, "windows", "x64"); err != nil {
		t.Fatalf("agent extraction: %v", err)
	}
	if counting.n != int64(len(data)) {
		t.Fatalf("agent extraction read %d bytes, want the full archive (%d)", counting.n, len(data))
	}

	counting2 := &countingReader{r: bytes.NewReader(data)}
	if _, err := selfupdate.DecompressCommand(counting2, fixtures.AssetName(nextVersion), fixtures.CliName, "windows", "x64"); err != nil {
		t.Fatalf("cli extraction: %v", err)
	}
	if counting2.n != int64(len(data)) {
		t.Fatalf("cli extraction read %d bytes, want the full archive (%d)", counting2.n, len(data))
	}
}

func extractOne(t *testing.T, data []byte, cmdName string) []byte {
	t.Helper()
	r, err := selfupdate.DecompressCommand(
		bytes.NewReader(data), fixtures.AssetName(nextVersion), cmdName, "windows", "x64")
	if err != nil {
		t.Fatalf("DecompressCommand(%s): %v", cmdName, err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read extracted %s: %v", cmdName, err)
	}
	return out
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func writeSingleExeZip(t *testing.T, path, root, exeName string, exeBytes []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	hdr := &zip.FileHeader{Name: root + "/bin/" + exeName, Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("zip header: %v", err)
	}
	if _, err := w.Write(exeBytes); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}
