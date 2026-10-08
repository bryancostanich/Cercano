package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"
	"unicode/utf8"
)

var (
	ErrProbeInvalidOptions = errors.New("bootstrap: invalid capability probe options")
	ErrProbeStart          = errors.New("bootstrap: capability probe could not start")
	ErrProbeTimedOut       = errors.New("bootstrap: capability probe timed out")
	ErrProbeOutputExceeded = errors.New("bootstrap: capability output exceeded limit")
	ErrProbeRefused        = errors.New("bootstrap: capability document refused")
)

type ProbeStatus int

const (
	ProbeExecutionReady ProbeStatus = iota + 1
	ProbeProtocolNotReady
	ProbeUnsupportedVersion
)

type ProbeResult struct {
	Status         ProbeStatus
	Protocol       int
	ExecutionReady bool
	Pid            int
}

// Env is a complete child environment, empty when nil. Windows Go may supply
// SystemRoot as required by the platform. No application credentials are selected.
type ProbeOptions struct {
	Timeout     time.Duration
	OutputLimit int64
	Env         []string
}

func (o ProbeOptions) resolve() (time.Duration, int64, error) {
	timeout, limit := o.Timeout, o.OutputLimit
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if limit == 0 {
		limit = 64 << 10
	}
	if timeout < 0 || timeout > time.Minute || limit < 0 || limit > 1<<20 {
		return 0, 0, ErrProbeInvalidOptions
	}
	return timeout, limit, nil
}

type cappedBuffer struct {
	buf   bytes.Buffer
	limit int64
	over  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - int64(c.buf.Len())
	n := len(p)
	if int64(n) > room {
		c.over = true
		p = p[:room]
	}
	_, _ = c.buf.Write(p)
	return n, nil // Drain excess instead of blocking or retaining it.
}

type probeDocument struct {
	component      string
	protocol       int
	executionReady bool
}

func parseProbeDocument(data []byte) (probeDocument, error) {
	var doc probeDocument
	if !utf8.Valid(data) {
		return doc, ErrProbeRefused
	}
	d := json.NewDecoder(bytes.NewReader(data))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return doc, ErrProbeRefused
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return doc, ErrProbeRefused
		}
		key, ok := tok.(string)
		if !ok || seen[key] {
			return doc, ErrProbeRefused
		}
		seen[key] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return doc, ErrProbeRefused
		}
		switch key {
		case "component":
			err = json.Unmarshal(value, &doc.component)
		case "protocol":
			err = json.Unmarshal(value, &doc.protocol)
		case "execution_ready":
			err = json.Unmarshal(value, &doc.executionReady)
		default:
			return doc, ErrProbeRefused
		}
		if err != nil {
			return doc, ErrProbeRefused
		}
	}
	tok, err = d.Token()
	if err != nil || tok != json.Delim('}') || len(seen) != 3 {
		return doc, ErrProbeRefused
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return doc, ErrProbeRefused
	}
	if doc.component != "cercano" || doc.protocol != 1 {
		return doc, ErrProbeRefused
	}
	return doc, nil
}

// Probe revalidates the staged receipt, then invokes only the known offline
// version command. It never invokes the private execution mode. Legacy version
// text yields unsupported; readiness false is not permission to run an update.
//
// Memory, time and pipe-drain waits are bounded. The owned child is reaped even
// on timeout. This is not a sandbox or a guarantee of killing descendants a
// compromised image might spawn; upstream image trust remains mandatory.
// Revalidation is not a lock against a hostile same-user filesystem replacement.
func Probe(ctx context.Context, image *PreparedImage, opts ProbeOptions) (ProbeResult, error) {
	if ctx == nil || image == nil {
		return ProbeResult{}, ErrProbeInvalidOptions
	}
	timeout, limit, err := opts.resolve()
	if err != nil {
		return ProbeResult{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err = image.Revalidate(probeCtx); err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return ProbeResult{}, ErrProbeTimedOut
		}
		if probeCtx.Err() != nil {
			return ProbeResult{}, errors.Join(ErrCanceled, probeCtx.Err())
		}
		return ProbeResult{}, err
	}
	cmd := exec.CommandContext(probeCtx, image.path, "version", "--updater-protocol")
	cmd.Env = append([]string{}, opts.Env...)
	cmd.WaitDelay = 100 * time.Millisecond
	out, stderr := &cappedBuffer{limit: limit}, &cappedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = out, stderr // nil stdin is the null device
	if err = cmd.Start(); err != nil {
		return ProbeResult{}, ErrProbeStart
	}
	result := ProbeResult{Pid: cmd.Process.Pid} // Diagnostic only: never signal after return.
	err = cmd.Wait()
	if probeCtx.Err() != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return result, ErrProbeTimedOut
		}
		return result, errors.Join(ErrCanceled, probeCtx.Err())
	}
	if out.over || stderr.over {
		return result, ErrProbeOutputExceeded
	}
	if err != nil {
		return result, ErrProbeRefused
	}
	data := bytes.TrimSpace(out.buf.Bytes())
	if len(data) == 0 {
		return result, ErrProbeRefused
	}
	if data[0] != '{' {
		// Only recognisable legacy version text is unsupported. Arbitrary non-JSON
		// output is a malformed reply, not evidence of a legitimate older helper.
		if utf8.Valid(data) && bytes.HasPrefix(data, []byte("cercano ")) && !bytes.ContainsAny(data, "\r\n") {
			result.Status = ProbeUnsupportedVersion
			return result, nil
		}
		return result, ErrProbeRefused
	}
	doc, err := parseProbeDocument(data)
	if err != nil {
		return result, err
	}
	result.Protocol, result.ExecutionReady = doc.protocol, doc.executionReady
	result.Status = ProbeProtocolNotReady
	if doc.executionReady {
		result.Status = ProbeExecutionReady
	}
	return result, nil
}
