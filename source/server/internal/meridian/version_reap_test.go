package meridian

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// TestEnsure_StaleExternalReaped: a version-mismatched external Meridian we have
// no pidfile for (reapOrphanFn false — the exact case that used to be adopted
// as External forever) is reaped and replaced with our pinned version.
func TestEnsure_StaleExternalReaped(t *testing.T) {
	m := newTestManager()
	var portUsed atomic.Bool
	portUsed.Store(true)
	m.portUsedFn = func(int) bool { return portUsed.Load() }
	m.reapOrphanFn = func(int) bool { return false } // not our pidfile-tracked orphan

	var probed, reaped int32
	m.versionProbeFn = func(context.Context, int) (string, bool) {
		atomic.AddInt32(&probed, 1)
		return "0.0.0-stale", true // != m.version
	}
	m.reapForeignFn = func(int) bool {
		atomic.AddInt32(&reaped, 1)
		portUsed.Store(false) // the kill frees the port so supervise can bind
		return true
	}
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&probed) != 1 {
		t.Errorf("version probed %d times, want 1", probed)
	}
	if atomic.LoadInt32(&reaped) != 1 {
		t.Errorf("reapForeign called %d times, want 1", reaped)
	}
	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times, want 1 (should replace the stale proxy)", spawned)
	}
	m.Stop()
}

// TestEnsure_SameVersionExternalAdopted: an external Meridian already on our
// pinned version is adopted as External, never reaped.
func TestEnsure_SameVersionExternalAdopted(t *testing.T) {
	m := newTestManager()
	m.portUsedFn = func(int) bool { return true }
	m.reapOrphanFn = func(int) bool { return false }
	m.versionProbeFn = func(context.Context, int) (string, bool) { return m.version, true }
	var reaped int32
	m.reapForeignFn = func(int) bool { atomic.AddInt32(&reaped, 1); return true }

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external (a same-version proxy is adopted)", got)
	}
	if atomic.LoadInt32(&reaped) != 0 {
		t.Errorf("reapForeign called %d times, want 0 for a same-version proxy", reaped)
	}
	m.Stop()
}

// TestEnsure_ForeignProxyAdopted: a proxy we can't identify as a Meridian
// (versionProbe ok=false — e.g. OpenCode) is adopted, never reaped.
func TestEnsure_ForeignProxyAdopted(t *testing.T) {
	m := newTestManager()
	m.portUsedFn = func(int) bool { return true }
	m.reapOrphanFn = func(int) bool { return false }
	m.versionProbeFn = func(context.Context, int) (string, bool) { return "", false }
	var reaped int32
	m.reapForeignFn = func(int) bool { atomic.AddInt32(&reaped, 1); return true }

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external", got)
	}
	if atomic.LoadInt32(&reaped) != 0 {
		t.Errorf("reapForeign called %d times, want 0 for an unidentifiable proxy", reaped)
	}
	m.Stop()
}

// TestEnsure_StaleButUnreapableAdopted: stale version identified, but the reap
// couldn't confirm/kill the group (reapForeign false). We adopt External rather
// than spawn a second proxy onto an occupied port, and the status message flags
// it as stale.
func TestEnsure_StaleButUnreapableAdopted(t *testing.T) {
	m := newTestManager()
	m.portUsedFn = func(int) bool { return true }
	m.reapOrphanFn = func(int) bool { return false }
	m.versionProbeFn = func(context.Context, int) (string, bool) { return "0.0.0-stale", true }
	m.reapForeignFn = func(int) bool { return false } // couldn't reap

	m.Ensure(context.Background(), 3456)

	st := m.Status()
	if st.State != StateExternal {
		t.Fatalf("state = %s, want external when a stale proxy can't be reaped", st.State)
	}
	if !strings.Contains(st.Message, "stale") {
		t.Errorf("status message = %q, want it to flag the stale proxy", st.Message)
	}
	m.Stop()
}

// TestEnsure_TrackedOrphanReapedWithoutVersionProbe: an orphan we DO have a
// pidfile for (reapOrphanFn true) is reaped+respawned as before, without a
// version probe — that path already refreshes to our pinned version.
func TestEnsure_TrackedOrphanReapedWithoutVersionProbe(t *testing.T) {
	m := newTestManager()
	var portUsed atomic.Bool
	portUsed.Store(true)
	m.portUsedFn = func(int) bool { return portUsed.Load() }
	m.reapOrphanFn = func(int) bool { portUsed.Store(false); return true } // our orphan; reap frees the port

	var probed int32
	m.versionProbeFn = func(context.Context, int) (string, bool) {
		atomic.AddInt32(&probed, 1)
		return "0.0.0-stale", true
	}
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&probed) != 0 {
		t.Errorf("version probed %d times, want 0 (the tracked-orphan path skips the probe)", probed)
	}
	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times, want 1", spawned)
	}
	m.Stop()
}
