// Package agentclient — reconnect.go: connection-state observer and
// automatic recovery when the agent server crashes.
//
// Cercano's CLI (and other clients) auto-launch a background agent server
// on first connect. If that server dies mid-session — panic, OOM, `kill`
// from the shell — the gRPC connection sees a transport EOF and every
// subsequent RPC returns codes.Unavailable. This file adds:
//
//   1. A ConnState enum and a channel where state transitions are
//      broadcast to observers (CLI status bar chip, etc.).
//   2. A background goroutine that watches the underlying gRPC conn and
//      kicks a reconnect when the state stays TRANSIENT_FAILURE for
//      more than a brief blip.
//   3. Client.reconnect: closes the dead conn, redials (may succeed if
//      the server was just slow), and on dial failure spawns a new
//      server via the existing autoLaunchServer helper.
//
// Bounded retry: 3 attempts with 1s / 2s / 4s backoff. Beyond that the
// client transitions to ConnStateFailed and the observer sees a final
// state event; further RPCs still error, but no more spawning.
package agentclient

import (
	"context"
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
	// stays set until we either land on Connected or exhaust retries.
	ConnStateReconnecting
	// ConnStateFailed is terminal for this Client — bounded retries were
	// exhausted. Further RPCs will fail; users should restart manually.
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

// maxReconnectAttempts bounds the retry loop. Beyond this the client
// transitions to Failed and stops spawning replacement servers. The
// backoff schedule (1s, 2s, 4s) sums to 7s of wait — enough for a slow
// restart, short enough that users notice the loop isn't stuck.
const maxReconnectAttempts = 3

// reconnectBackoff returns the sleep before the Nth attempt (1-indexed).
// Simple exponential: 1s, 2s, 4s.
func reconnectBackoff(attempt int) time.Duration {
	return time.Duration(1<<(attempt-1)) * time.Second
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
// the dead conn, redial (short timeout), and if dial fails spawn a
// replacement server via the existing autoLaunchServer helper. On
// success it swaps the underlying conn + agent atomically and emits a
// Connected transition. On exhaustion it emits Failed.
func (c *Client) reconnect(ctx context.Context) error {
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		c.setState(ConnStateReconnecting, attempt, nil)
		time.Sleep(reconnectBackoff(attempt))

		// Close the old conn so any dangling streams see EOF and their
		// drain loops can restart. Best-effort — a failing close doesn't
		// stop us from opening a new one.
		if old := c.readConn(); old != nil {
			_ = old.Close()
		}

		// Try a short redial first — the server may have simply been
		// slow. If that fails we escalate to spawning.
		fresh, err := connect(ctx, c.addr, 2*time.Second)
		if err != nil {
			// Server truly appears gone; spawn a fresh one.
			if _, spawnErr := autoLaunchServer(c.addr); spawnErr != nil {
				// Spawn itself failed (missing binary, permission,
				// etc.) — no point retrying spawn on next attempt, but
				// let the backoff run so the port check gets another
				// shot in case the OLD server is coming back up.
				continue
			}
			if err := waitForPort(c.addr, 8*time.Second); err != nil {
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
	c.setState(ConnStateFailed, maxReconnectAttempts, fmt.Errorf("agentclient: exhausted %d reconnect attempts", maxReconnectAttempts))
	return fmt.Errorf("agentclient: reconnect failed after %d attempts", maxReconnectAttempts)
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
