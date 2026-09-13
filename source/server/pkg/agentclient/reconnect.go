// Package agentclient — reconnect.go: connection-state observer and
// automatic recovery when the agent server crashes.
//
// Cercano's CLI (and other clients) auto-launch a background agent server
// on first connect. If that server dies mid-session — panic, OOM, `kill`
// from the shell — the gRPC connection sees a transport EOF and every
// subsequent RPC returns codes.Unavailable. This file adds:
//
//  1. A ConnState enum and a channel where state transitions are
//     broadcast to observers (CLI status bar chip, etc.).
//  2. A background goroutine that watches the underlying gRPC conn and
//     kicks a reconnect when the state stays TRANSIENT_FAILURE for
//     more than a brief blip.
//  3. Client.reconnect: closes the dead conn, redials (may succeed if
//     the server was just slow), and on dial failure spawns a new
//     server via the existing autoLaunchServer helper.
//
// Two-phase retry: a fast burst (3 attempts, 1s / 2s / 4s backoff) for
// transient blips, then an indefinite slow lane — redial every 10s until
// the server comes back or the client shuts down. Respawn attempts are
// throttled in the slow lane (every 3rd try) so a genuinely broken
// binary doesn't fork-bomb. The client never gives up on its own:
// ConnStateFailed is reserved for client shutdown mid-recovery, not
// retry exhaustion — a healthy server appearing minutes later is
// always picked up. Recovery is single-flighted across callers.
package agentclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	"cercano/source/server/internal/crashlog"
	"cercano/source/server/pkg/proto"
)

// defaultCrashLogPath returns the standard crash-log location for
// Cercano — matches what runServerMode writes to. Reproduced here so
// agentclient doesn't need to depend on the config package (which
// would enlarge the SDK's dependency footprint for embedders).
func defaultCrashLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cercano", "crash.log")
}

// ConnState is the coarse-grained connection health that observers care
// about. gRPC's own connectivity states are more granular (IDLE / CONNECTING
// / READY / TRANSIENT_FAILURE / SHUTDOWN); this collapses them to the three
// buckets a status-bar chip actually needs to render.
type ConnState int

const (
	// ConnStateConnected is the steady-state healthy connection.
	ConnStateConnected ConnState = iota
	// ConnStateReconnecting is set the moment we detect a dead conn and
	// stays set until recovery lands on Connected. The recovery loop
	// retries indefinitely (fast burst, then a 10s slow lane), so this
	// state can persist for as long as the server stays down.
	ConnStateReconnecting
	// ConnStateFailed is terminal for this Client — emitted only when
	// the client itself shuts down mid-recovery (context cancelled or
	// Close called), never from retry exhaustion.
	ConnStateFailed
)

// String makes state values readable in logs and test failures.
func (s ConnState) String() string {
	switch s {
	case ConnStateConnected:
		return "connected"
	case ConnStateReconnecting:
		return "reconnecting"
	case ConnStateFailed:
		return "failed"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// ConnStateChanged is the message pushed to observers on every state
// transition. Attempt is 0 for the initial Connected event and increments
// per reconnect attempt so UIs can render "reconnecting (2/3)…". Err is
// non-nil only on the transition into ConnStateFailed.
//
// CrashSummary is set on the FIRST transition into Reconnecting after a
// server death — the reconnect flow reads the most recent line from
// ~/.config/cercano/crash.log (if present) so the CLI can tell the
// user WHY the server died, not just that it did. Empty when the log
// is missing, on subsequent Reconnecting transitions within the same
// recovery cycle, or when the state change wasn't triggered by a crash.
type ConnStateChanged struct {
	State        ConnState
	Attempt      int
	Err          error
	CrashSummary string
}

// FastReconnectAttempts is the size of the fast burst: quick exponential
// retries meant to ride out transient blips. Exported so UIs can render
// "reconnecting (N/3)…" during the burst and switch copy once recovery
// enters the slow lane.
const FastReconnectAttempts = 3

// slowRetryInterval is the steady cadence after the fast burst. The
// client redials at this interval indefinitely — a server that comes
// back minutes later is still picked up.
const slowRetryInterval = 10 * time.Second

// slowRespawnEvery throttles server spawning in the slow lane: a spawn
// is attempted on the first slow try and every Nth after that, while
// the (cheap) redial happens on every interval. Keeps a broken binary
// from being exec'd six times a minute forever.
const slowRespawnEvery = 3

// errClientClosed is returned by reconnect when the client shuts down
// mid-recovery; it is the only non-ctx path that emits ConnStateFailed.
var errClientClosed = errors.New("agentclient: client closed during reconnect")

// reconnectWait returns the sleep before the Nth attempt (1-indexed):
// 1s / 2s / 4s for the fast burst, then the steady slow-lane interval.
func reconnectWait(attempt int) time.Duration {
	if attempt <= FastReconnectAttempts {
		return time.Duration(1<<(attempt-1)) * time.Second
	}
	return slowRetryInterval
}

// shouldRespawn reports whether the Nth attempt (1-indexed) should
// escalate a failed dial to spawning a replacement server. Every
// fast-burst attempt spawns (the original behavior); slow-lane attempts
// spawn on the first and then every slowRespawnEvery-th.
func shouldRespawn(attempt int) bool {
	if attempt <= FastReconnectAttempts {
		return true
	}
	return (attempt-FastReconnectAttempts-1)%slowRespawnEvery == 0
}

// isUnavailable reports whether err carries the gRPC codes.Unavailable
// status — the sentinel we treat as "server may be dead". Any other
// error (auth, InvalidArgument, DeadlineExceeded on a specific call)
// bubbles up unmodified.
func isUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.Unavailable
	}
	return false
}

// stateBroker fan-outs ConnStateChanged events to any number of
// subscribers. Subscribers are non-blocking: a slow reader misses events
// rather than backpressuring the broker. Concurrent Subscribe /
// broadcast / unsubscribe are safe.
type stateBroker struct {
	mu   sync.Mutex
	subs map[chan ConnStateChanged]struct{}
}

func newStateBroker() *stateBroker {
	return &stateBroker{subs: make(map[chan ConnStateChanged]struct{})}
}

func (b *stateBroker) subscribe() (<-chan ConnStateChanged, func()) {
	ch := make(chan ConnStateChanged, 8)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	unsubscribe := func() {
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}
	return ch, unsubscribe
}

func (b *stateBroker) broadcast(ev ConnStateChanged) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber's buffer is full — drop rather than block the
			// broker. A missed intermediate event is fine; the next one
			// carries the current state.
		}
	}
}

// ConnStateChanges returns a channel that receives ConnStateChanged
// events for the lifetime of the Client. The unsubscribe func must be
// called when the observer is done to release the buffer.
func (c *Client) ConnStateChanges() (<-chan ConnStateChanged, func()) {
	if c.stateBroker == nil {
		c.stateBroker = newStateBroker()
	}
	return c.stateBroker.subscribe()
}

// currentState returns the last-published state. Used by tests and by
// the SubscribeEvents drain loop to decide whether to wait for a
// reconnect or bail out.
func (c *Client) currentState() ConnState {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.state
}

// State reports the current connection health as tracked by the reconnect
// watcher. Interactive clients use it to fail RPC-backed actions fast while
// the agent is restarting, instead of burning a call deadline on a
// connection that is known to be down.
func (c *Client) State() ConnState { return c.currentState() }

// setState atomically updates and broadcasts the new state. No-op if the
// state hasn't actually changed (silences duplicate events).
//
// On a Connected → Reconnecting transition (which means the transport
// just died), this method also reads the tail of the crash log so the
// event carries a short summary of WHY the server died. Attach is
// best-effort; a missing log or malformed entry just means no summary.
func (c *Client) setState(next ConnState, attempt int, err error) {
	c.stateMu.Lock()
	prev := c.state
	changed := prev != next
	c.state = next
	c.stateMu.Unlock()
	if !changed {
		return
	}
	if c.stateBroker == nil {
		c.stateBroker = newStateBroker()
	}
	ev := ConnStateChanged{State: next, Attempt: attempt, Err: err}
	if prev == ConnStateConnected && next == ConnStateReconnecting {
		ev.CrashSummary = crashlog.LatestSummary(defaultCrashLogPath())
	}
	c.stateBroker.broadcast(ev)
}

// watchConn is the background goroutine spawned in Dial. It observes the
// gRPC connection's connectivity state and kicks a reconnect when the
// underlying transport stays in TRANSIENT_FAILURE past a short blip
// threshold. Exits when ctx is cancelled or the client's stopWatch
// channel closes.
func (c *Client) watchConn(ctx context.Context) {
	// Kick off in Connected state so the initial Dial success is
	// reflected without a redundant event.
	c.setState(ConnStateConnected, 0, nil)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopWatch:
			return
		default:
		}
		conn := c.readConn()
		if conn == nil {
			// Client shutting down; exit.
			return
		}
		state := conn.GetState()
		if state == connectivity.Shutdown {
			return
		}
		if state == connectivity.TransientFailure {
			// Give it a beat to self-heal (gRPC will retry the
			// transport on its own). If still failing, escalate.
			time.Sleep(500 * time.Millisecond)
			if conn.GetState() == connectivity.TransientFailure {
				if err := c.reconnect(ctx); err != nil {
					// reconnect() sets ConnStateFailed on its final
					// attempt — no further action here.
					return
				}
			}
			continue
		}
		// Block until state changes; avoids a busy loop.
		conn.WaitForStateChange(ctx, state)
	}
}

// reconnect performs the actual recovery loop: on each attempt, close
// the dead conn, redial (short timeout), and — on the throttle
// schedule — spawn a replacement server via autoLaunchServer. Fast
// burst first, then the indefinite slow lane (see the package comment).
// Returns nil once a fresh conn is installed. It errors only when the
// client is shutting down (ctx cancelled / Close called), which is also
// the only path that emits ConnStateFailed.
func (c *Client) reconnect(ctx context.Context) error {
	// Single-flight: watchConn and the SubscribeEvents drain can both
	// detect the dead transport and call here concurrently. Whoever
	// loses the race waits, then returns immediately if the winner
	// already restored the connection.
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	if c.currentState() == ConnStateConnected {
		if conn := c.readConn(); conn != nil && conn.GetState() == connectivity.Ready {
			return nil
		}
	}
	for attempt := 1; ; attempt++ {
		c.setState(ConnStateReconnecting, attempt, nil)
		select {
		case <-ctx.Done():
			c.setState(ConnStateFailed, attempt, ctx.Err())
			return ctx.Err()
		case <-c.stopWatch:
			c.setState(ConnStateFailed, attempt, errClientClosed)
			return errClientClosed
		case <-time.After(reconnectWait(attempt)):
		}

		// Close the old conn so any dangling streams see EOF and their
		// drain loops can restart. Best-effort — a failing close doesn't
		// stop us from opening a new one.
		if old := c.readConn(); old != nil {
			_ = old.Close()
		}

		// Try a short redial first — the server may have simply been
		// slow. If that fails, escalate to spawning on the throttle
		// schedule.
		fresh, err := connect(ctx, c.addr, 2*time.Second)
		if err != nil {
			if !shouldRespawn(attempt) {
				continue
			}
			if _, _, launchErr := ensureServerLaunched(ctx, c.addr, 8*time.Second); launchErr != nil {
				// Launch itself failed (missing binary, permission, slow
				// bind, etc.) — the next scheduled attempt gets another
				// shot in case the OLD server is coming back up.
				continue
			}
			fresh, err = connect(ctx, c.addr, 3*time.Second)
			if err != nil {
				continue
			}
		}

		// Swap the underlying conn + agent handle atomically so
		// in-flight callers using c.agent see the new client on their
		// next call.
		c.writeConn(fresh.conn, fresh.agent)
		c.setState(ConnStateConnected, attempt, nil)
		return nil
	}
}

// readConn returns the current gRPC conn under the connection lock.
// nil means the Client is being torn down or was never fully dialed.
func (c *Client) readConn() *grpcConn {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn
}

// writeConn swaps the conn + agent under the connection lock. Used by
// reconnect() to atomically install a fresh connection so subsequent
// RPC calls hit the new server.
func (c *Client) writeConn(conn *grpcConn, agent proto.AgentClient) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.conn = conn
	c.agent = agent
}

// grpcConn is a package-local alias so the readConn/writeConn helpers
// don't need to import the whole grpc package for their signatures.
// The actual type is *grpc.ClientConn, exposed via a type alias in
// client.go where the grpc import already lives.
type grpcConn = grpcConnAlias
