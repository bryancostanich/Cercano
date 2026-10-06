package main

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"testing"
)

type versionProbeTransport struct{ calls int }

func (p *versionProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	p.calls++
	return nil, errors.New("network forbidden in version command")
}

// Run the real entrypoint without allowing HTTP or touching agent startup.
func TestReleaseVersionOffline(t *testing.T) {
	for _, arg := range []string{"--version"} {
		t.Run(arg, func(t *testing.T) {
			oldArgs, oldFlags, oldTransport, oldStdout := os.Args, flag.CommandLine, http.DefaultTransport, os.Stdout
			defer func() {
				os.Args, flag.CommandLine, http.DefaultTransport, os.Stdout = oldArgs, oldFlags, oldTransport, oldStdout
			}()
			os.Args = []string{"cercano-cli", arg}
			flag.CommandLine = flag.NewFlagSet("cercano-cli", flag.ContinueOnError)
			probe := &versionProbeTransport{}
			http.DefaultTransport = probe
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			os.Stdout = w
			main()
			w.Close()
			out, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if probe.calls != 0 {
				t.Errorf("version command attempted %d HTTP requests", probe.calls)
			}
			if want := "cercano-cli v" + version + "\n"; string(out) != want {
				t.Errorf("output = %q, want %q", out, want)
			}
		})
	}
}
