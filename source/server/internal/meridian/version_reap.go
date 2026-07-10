package meridian

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// versionProbeTimeout bounds the /health request the manager makes to learn a
// running proxy's version. Kept short because the probe runs under the manager
// lock during Ensure and the proxy is local: a bound proxy answers in
// milliseconds, and an unresponsive one must not stall startup.
var versionProbeTimeout = 1 * time.Second

// realVersionProbe asks the proxy already on the port for its version via
// Meridian's /health endpoint. That endpoint is auth-exempt and always carries
// a top-level "version" field — even on a 503 not-logged-in response — so we do
// not gate on the status code. It returns ok=false when the endpoint is
// unreachable, isn't the expected JSON, or reports no concrete version
// ("unknown"): i.e. whenever we cannot positively identify a Meridian to
// compare against DefaultVersion. Callers MUST treat ok=false as "don't touch
// it" — it may be OpenCode or some other proxy we have no business killing.
func realVersionProbe(ctx context.Context, port int) (version string, ok bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://127.0.0.1:"+strconv.Itoa(port)+"/health", nil)
	if err != nil {
		return "", false
	}
	client := &http.Client{Timeout: versionProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return "", false
	}
	if body.Version == "" || body.Version == "unknown" {
		return "", false
	}
	return body.Version, true
}

// realReapForeign kills a Meridian process group holding the port when there is
// no pidfile identifying it as ours (a pre-lock orphan, or a build predating
// pidfiles — exactly the case realReapOrphan returns false for). It is only
// called after realVersionProbe positively identified a STALE Meridian AND the
// caller holds the spawn lock, so it cannot start a sibling reap-war. It finds
// the pid listening on the port, confirms the process group still looks like
// Meridian, and SIGKILLs the whole group. Returns true only when it actually
// killed a confirmed Meridian group.
func (m *Manager) realReapForeign(port int) bool {
	pid := pidOnPort(port)
	if pid == 0 {
		return false
	}
	pgid := processGroupID(pid)
	if pgid == 0 {
		pgid = pid
	}
	// Never kill what we cannot identify as a Meridian group: a recycled pid,
	// or a non-Meridian proxy that happened to answer with version-shaped JSON.
	if !m.identifyGroupFn(pgid) {
		m.logger.Printf("meridian: pid %d on port %d is not a Meridian group; leaving it alone", pid, port)
		return false
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		m.logger.Printf("meridian: failed to kill stale Meridian group %d on port %d: %v", pgid, port, err)
		return false
	}
	return true
}

// pidOnPort returns the pid LISTENing on 127.0.0.1:port, or 0. The port-holder
// is Meridian's node process (the npx → node chain). Uses lsof.
func pidOnPort(port int) int {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			return pid
		}
	}
	return 0
}

// processGroupID returns the process-group id (pgid) of pid, or 0. realSpawn
// puts the npx → Meridian chain in its own group keyed by the npx pid, so
// killing by pgid takes down both the npx wrapper and the node grandchild —
// killing only the listener would leave the wrapper (or vice versa) behind.
func processGroupID(pid int) int {
	out, err := exec.Command("ps", "-o", "pgid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return pgid
}
