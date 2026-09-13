package usage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"cercano/source/server/internal/llm"
)

// Attribution contains only recording metadata; never prompts or credentials.
type Attribution struct {
	OperationID    string `json:"operation_id"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
	WorkerID       string `json:"worker_id"`
	Source         string `json:"source"`
}

type Outcome string

const (
	Started     Outcome = "started"
	Completed   Outcome = "completed"
	Failed      Outcome = "failed"
	Interrupted Outcome = "interrupted"
)

// AttemptObservation is an immutable value snapshot. Identity+Revision permit
// repeated or out-of-order delivery without counting a model call twice.
// Final counts can still be unknown (e.g. canceled before usage was returned).
type AttemptObservation struct {
	ID          string         `json:"id"`
	Revision    uint64         `json:"revision"`
	Attribution Attribution    `json:"attribution"`
	Provider    string         `json:"provider"`
	Model       string         `json:"model"`
	Profile     string         `json:"profile"`
	Destination string         `json:"destination"`
	StartedAt   time.Time      `json:"started_at"`
	EndedAt     time.Time      `json:"ended_at"`
	Outcome     Outcome        `json:"outcome"`
	Tokens      llm.TokenUsage `json:"tokens"`
}

// AttemptSink must only perform bounded in-memory admission. No storage, wire
// serialization, transport writes, or acknowledgments belong in this callback.
// False is not permission to hide loss: the sink owns independent loss/health
// counters so overload remains visible even when its event queue is full.
type AttemptSink func(AttemptObservation) bool

type attemptContext struct {
	sink        AttemptSink
	attribution Attribution
}
type attemptContextKey struct{}

// WithAttempts extends existing sink wiring to physical adapter boundaries.
// Higher-level wrappers supply attribution but must not emit duplicate attempts.
func WithAttempts(ctx context.Context, sink AttemptSink, attribution Attribution) context.Context {
	return context.WithValue(ctx, attemptContextKey{}, attemptContext{sink, attribution})
}

var attemptSequence atomic.Uint64
var attemptProcessID = func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("usage: cannot initialize attempt identity")
	}
	return hex.EncodeToString(b[:])
}()

// NewIdentity supplies a process-unique stable accounting record identity.
func NewIdentity() string {
	return attemptProcessID + "-" + strconv.FormatUint(attemptSequence.Add(1), 10)
}

// Attempt owns one physical call. Retrying adapters must StartAttempt again for
// every request; SDK retries require instrumentation inside the SDK retry loop.
type Attempt struct {
	mu          sync.Mutex
	observation AttemptObservation
	sink        AttemptSink
}

func StartAttempt(ctx context.Context, provider, model string) *Attempt {
	config, _ := ctx.Value(attemptContextKey{}).(attemptContext)
	if config.sink == nil {
		return nil
	}
	a := &Attempt{sink: config.sink, observation: AttemptObservation{
		ID:       NewIdentity(),
		Revision: 1, Attribution: config.attribution, Provider: provider, Model: model,
		StartedAt: time.Now().UTC(), Outcome: Started,
	}}
	a.sink(a.observation)
	return a
}

// Observe incorporates cumulative usage and the actual serving route. It does
// not publish text-sized per-token events; usage snapshots are published when
// providers report usage, and again at finalization.
func (a *Attempt) Observe(tokens llm.TokenUsage, route *llm.ServingRoute) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.observation.Outcome != Started {
		a.mu.Unlock()
		return
	}
	before := a.observation
	a.observation.Tokens = a.observation.Tokens.Merge(tokens)
	if route != nil {
		if route.Provider != "" {
			a.observation.Provider = route.Provider
		}
		if route.Model != "" {
			a.observation.Model = route.Model
		}
		a.observation.Profile = route.Profile
		a.observation.Destination = route.Destination
	}
	if a.observation == before {
		a.mu.Unlock()
		return
	}
	a.observation.Revision++
	snapshot := a.observation
	a.mu.Unlock()
	a.sink(snapshot)
}

// Finish is idempotent across terminal signals, errors, and repeated Close.
// Emission is outside the short metadata lock; storage orders revisions rather
// than assuming concurrent callbacks arrive in revision order.
func (a *Attempt) Finish(outcome Outcome) {
	if a == nil {
		return
	}
	if outcome != Completed && outcome != Failed && outcome != Interrupted {
		return
	}
	a.mu.Lock()
	if a.observation.Outcome != Started {
		a.mu.Unlock()
		return
	}
	a.observation.Outcome = outcome
	a.observation.EndedAt = time.Now().UTC()
	a.observation.Revision++
	snapshot := a.observation
	a.mu.Unlock()
	a.sink(snapshot)
}

// AttemptsEnabled lets adapters avoid installing optional SDK hooks when no
// accounting sink is attached to the request context.
func AttemptsEnabled(ctx context.Context) bool {
	config, _ := ctx.Value(attemptContextKey{}).(attemptContext)
	return config.sink != nil
}

// ForOperation derives fresh work identity while preserving parent attribution.
// A nil sink inherits the existing context sink; it does not disable accounting.
// Background entry points supply their configured sink and conversation ID.
func ForOperation(ctx context.Context, sink AttemptSink, source, conversation string) context.Context {
	inherited, _ := ctx.Value(attemptContextKey{}).(attemptContext)
	if sink != nil {
		inherited.sink = sink
	}
	if inherited.sink == nil {
		return ctx
	}
	a := inherited.attribution
	if source != "" {
		a.Source = source
	}
	if conversation != "" {
		a.ConversationID = conversation
	}
	a.OperationID = NewIdentity()
	return WithAttempts(ctx, inherited.sink, a)
}

// AttributionFromContext copies metadata for existing in-process/RPC boundaries.
// The sink itself never crosses a process boundary.
func AttributionFromContext(ctx context.Context) (Attribution, bool) {
	config, ok := ctx.Value(attemptContextKey{}).(attemptContext)
	if !ok || config.sink == nil {
		return Attribution{}, false
	}
	return config.attribution, true
}
