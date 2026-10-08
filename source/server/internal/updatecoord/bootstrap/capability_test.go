package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/oneshot"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("CERCANO_BOOTSTRAP_PROBE_FIXTURE"); mode != "" {
		if len(os.Args) != 3 || os.Args[1] != "version" || os.Args[2] != "--updater-protocol" {
			os.Exit(9)
		}
		switch mode {
		case "ready":
			fmt.Print(`{"component":"cercano","protocol":1,"execution_ready":true}`)
		case "notready":
			fmt.Print(`{"component":"cercano","protocol":1,"execution_ready":false}`)
		case "old":
			fmt.Println("cercano v0.1.0")
		case "noise":
			fmt.Print(strings.Repeat("x", 8192))
		case "sleep":
			time.Sleep(10 * time.Second)
		default:
			os.Exit(8)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestProbeDocumentStrict(t *testing.T) {
	valid := `{"component":"cercano","protocol":1,"execution_ready":false}`
	if d, e := parseProbeDocument([]byte(valid)); e != nil || d.executionReady {
		t.Fatal(d, e)
	}
	for _, bad := range []string{
		`{}`, `null`, valid + ` {}`, valid + `secret`,
		strings.Replace(valid, `false`, `null`, 1), strings.Replace(valid, `false`, `"false"`, 1),
		strings.Replace(valid, `:1`, `:1.0`, 1), strings.Replace(valid, `:1`, `:2`, 1),
		strings.Replace(valid, `"protocol":1`, `"protocol":1,"protocol":1`, 1),
		strings.Replace(valid, `"component"`, `"Component"`, 1),
		strings.Replace(valid, `"cercano"`, `"other"`, 1),
		strings.Replace(valid, `"protocol":1,`, ``, 1),
		strings.Replace(valid, `"protocol":1,`, `"protocol":1,"extra":true,`, 1),
		strings.Replace(valid, `"cercano"`, "\"cercano\xff\"", 1),
	} {
		if _, e := parseProbeDocument([]byte(bad)); !errors.Is(e, ErrProbeRefused) {
			t.Errorf("accepted invalid document %q: %v", bad, e)
		}
	}
}
func TestProbeOwnedImage(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(data)
	root := t.TempDir()
	image, e := Prepare(context.Background(), Request{Oneshot: oneshot.Request{InstallID: "probe-test", OperationID: 1}, Source: Source{Path: exe, ExpectedSHA256: hex.EncodeToString(hash[:]), ExpectedLength: int64(len(data))}, StateRoot: root, ForbiddenRoots: []string{filepath.Dir(exe)}})
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		mode    string
		status  ProbeStatus
		err     error
		timeout time.Duration
	}{
		{"ready", ProbeExecutionReady, nil, 0}, {"notready", ProbeProtocolNotReady, nil, 0}, {"old", ProbeUnsupportedVersion, nil, 0},
		{"noise", 0, ErrProbeOutputExceeded, 0}, {"sleep", 0, ErrProbeTimedOut, 100 * time.Millisecond},
	} {
		t.Run(test.mode, func(t *testing.T) {
			got, e := Probe(context.Background(), image, ProbeOptions{Env: []string{"CERCANO_BOOTSTRAP_PROBE_FIXTURE=" + test.mode}, OutputLimit: 1024, Timeout: test.timeout})
			if test.err != nil {
				if !errors.Is(e, test.err) {
					t.Fatalf("got %+v %v", got, e)
				}
				return
			}
			if e != nil || got.Status != test.status || got.ExecutionReady != (test.status == ProbeExecutionReady) {
				t.Fatalf("got %+v %v", got, e)
			}
		})
	}
	// Replacement with identical bytes still violates the captured image identity.
	replacement := filepath.Join(image.StagingDir(), "replacement")
	if e = os.WriteFile(replacement, data, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(replacement, image.Path()); e != nil {
		t.Fatal(e)
	}
	if result, e := Probe(context.Background(), image, ProbeOptions{}); !errors.Is(e, ErrReceiptStale) || result.Pid != 0 {
		t.Fatalf("replaced image executed: %+v %v", result, e)
	}
}
func TestProbeRevalidationTamperAndOptions(t *testing.T) {
	root, forbidden := stateRootFixture(t)
	source, digest, length := makeSource(t, forbidden, "inert-source", 32)
	req := validRequest(t, root, forbidden, source, digest, length)
	image, e := Prepare(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if e = image.Revalidate(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(image.Path(), []byte("changed"), 0700); e != nil {
		t.Fatal(e)
	}
	if r, e := Probe(context.Background(), image, ProbeOptions{}); !errors.Is(e, ErrReceiptStale) || r.Pid != 0 {
		t.Fatalf("tampered image executed: %+v %v", r, e)
	}
	if _, e := Probe(context.Background(), image, ProbeOptions{Timeout: -1}); !errors.Is(e, ErrProbeInvalidOptions) {
		t.Fatal(e)
	}
}
