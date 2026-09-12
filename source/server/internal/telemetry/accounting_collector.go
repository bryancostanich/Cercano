package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cercano/source/server/internal/usage"
)

type AttemptStore interface {
	InitializeAccounting(context.Context) error
	WriteAttempts(context.Context, []usage.AttemptObservation) error
}

type AccountingHealth struct {
	CoverageIncomplete bool      `json:"coverage_incomplete"`
	Accepted           uint64    `json:"accepted"`
	Persisted          uint64    `json:"persisted"`
	Lost               uint64    `json:"lost"`
	Pending            int       `json:"pending"`
	Retries            uint64    `json:"retries"`
	WriteFailures      uint64    `json:"write_failures"`
	Uncertain          uint64    `json:"uncertain"`
	OldestPending      time.Time `json:"oldest_pending"`
	LastPersistence    time.Time `json:"last_persistence"`
	LastError          string    `json:"last_error"`
	Closed             bool      `json:"closed"`
}

type AccountingOptions struct {
	Capacity      int
	BatchSize     int
	FlushInterval time.Duration
	WriteTimeout  time.Duration
	RetryDelay    time.Duration
	MaxRetries    int
}

// Defaults assume ten simultaneous turns, two attempts/second each, and four
// usage snapshots/attempt: 80 observations/second. 1024 outstanding observations
// cover 12.8 seconds of stalled persistence. Metadata is capped at 10 KiB/event:
// about 10 MiB logical payload plus structs, maps and allocator overhead. Batches
// cap at 64; at a 50 ms flush interval the no-I/O ceiling is 1280 observations/s.
// These are sizing assumptions, not benchmark results or a durability guarantee.
func defaultAccountingOptions(o AccountingOptions) AccountingOptions {
	if o.Capacity <= 0 {
		o.Capacity = 1024
	}
	if o.Capacity > 4096 {
		o.Capacity = 4096
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 64
	}
	if o.BatchSize > 256 {
		o.BatchSize = 256
	}
	if o.BatchSize > o.Capacity {
		o.BatchSize = o.Capacity
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = 50 * time.Millisecond
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 2 * time.Second
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = 100 * time.Millisecond
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 8
	}
	if o.MaxRetries > 16 {
		o.MaxRetries = 16
	}
	return o
}

type queuedObservation struct {
	sequence    uint64
	observation usage.AttemptObservation
}

// AccountingCollector extends the existing telemetry collection subsystem with
// an independent bounded admission lane for attempt observations. All storage
// happens on one background goroutine. Outstanding capacity includes the batch
// currently being retried, not only the channel contents.
type AccountingCollector struct {
	writerID   string
	lastHealth *AccountingHealth
	store      AttemptStore
	options    AccountingOptions
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	health     AccountingHealth
	pending    map[uint64]time.Time
	queue      chan queuedObservation
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
}

func NewAccountingCollector(store AttemptStore, options AccountingOptions) *AccountingCollector {
	options = defaultAccountingOptions(options)
	ctx, cancel := context.WithCancel(context.Background())
	c := &AccountingCollector{writerID: usage.NewIdentity(), store: store, options: options, ctx: ctx, cancel: cancel, pending: make(map[uint64]time.Time), queue: make(chan queuedObservation, options.Capacity), stop: make(chan struct{}), done: make(chan struct{})}
	go c.run()
	return c
}

// Emit is only validation and bounded in-memory handoff. Loss counters do not
// share the event queue, so saturation cannot suppress its own health signal.
func (c *AccountingCollector) Emit(a usage.AttemptObservation) bool {
	err := ValidateAttempt(a)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || c.health.Closed || len(c.pending) >= c.options.Capacity {
		c.health.Lost++
		switch {
		case err != nil:
			c.health.LastError = err.Error()
		case c.health.Closed:
			c.health.LastError = "accounting closed"
		default:
			c.health.LastError = "accounting capacity exhausted"
		}
		return false
	}
	c.health.Accepted++
	seq := c.health.Accepted
	c.pending[seq] = time.Now().UTC()
	// The pending limit reserves space even while a batch is out of the channel.
	// Thus queue admission cannot block while holding this short metadata lock.
	c.queue <- queuedObservation{sequence: seq, observation: a}
	return true
}

func (c *AccountingCollector) Health() AccountingHealth {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.health
	out.Pending = len(c.pending)
	for _, at := range c.pending {
		if out.OldestPending.IsZero() || at.Before(out.OldestPending) {
			out.OldestPending = at
		}
	}
	return out
}

// Close stops admission and drains within the caller's deadline. If a store
// ignores cancellation, only the one writer can remain blocked; inference and
// shutdown still return. Unacknowledged observations remain explicitly uncertain.
func (c *AccountingCollector) Close(ctx context.Context) error {
	c.closeOnce.Do(func() { c.mu.Lock(); c.health.Closed = true; c.mu.Unlock(); close(c.stop) })
	select {
	case <-c.done:
		c.cancel()
		return nil
	case <-ctx.Done():
		c.cancel()
		c.mu.Lock()
		c.health.Uncertain = uint64(len(c.pending))
		c.health.LastError = "accounting shutdown incomplete"
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *AccountingCollector) run() {
	defer close(c.done)
	ticker := time.NewTicker(c.options.FlushInterval)
	defer ticker.Stop()
	initialized := false
	draining := false
	for {
		if !draining {
			select {
			case <-c.ctx.Done():
				return
			case <-c.stop:
				draining = true
			case <-ticker.C:
			}
		}
		batch := make([]queuedObservation, 0, c.options.BatchSize)
		for len(batch) < c.options.BatchSize {
			select {
			case q := <-c.queue:
				batch = append(batch, q)
			default:
				goto ready
			}
		}
	ready:
		if len(batch) == 0 {
			if !initialized {
				ctx, cancel := context.WithTimeout(c.ctx, c.options.WriteTimeout)
				err := c.store.InitializeAccounting(ctx)
				cancel()
				if err == nil {
					initialized = true
				} else {
					c.mu.Lock()
					c.health.WriteFailures++
					c.health.LastError = "accounting initialization failed"
					c.mu.Unlock()
				}
			}
			if initialized {
				c.persistHealth()
			}
			if draining {
				return
			}
			continue
		}
		observations := make([]usage.AttemptObservation, len(batch))
		for i, q := range batch {
			observations[i] = q.observation
		}
		var err error
		for attempt := 0; attempt <= c.options.MaxRetries; attempt++ {
			ctx, cancel := context.WithTimeout(c.ctx, c.options.WriteTimeout)
			if !initialized {
				err = c.store.InitializeAccounting(ctx)
				if err == nil {
					initialized = true
				}
			}
			if initialized {
				err = c.store.WriteAttempts(ctx, observations)
			}
			cancel()
			if err == nil {
				break
			}
			c.mu.Lock()
			c.health.WriteFailures++
			c.health.LastError = "accounting persistence failed"
			c.mu.Unlock()
			if c.ctx.Err() != nil {
				return
			}
			if attempt == c.options.MaxRetries {
				break
			}
			c.mu.Lock()
			c.health.Retries++
			c.mu.Unlock()
			delay := c.options.RetryDelay
			for n := 0; n < attempt && delay < 2*time.Second; n++ {
				delay *= 2
			}
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		c.mu.Lock()
		for _, q := range batch {
			delete(c.pending, q.sequence)
		}
		if err == nil {
			c.health.Persisted += uint64(len(batch))
			c.health.LastPersistence = time.Now().UTC()
			if !c.health.CoverageIncomplete && c.health.Lost == 0 && c.health.Uncertain == 0 {
				c.health.LastError = ""
			}
		} else {
			// Failed acknowledgments do not establish whether a commit reached disk.
			c.health.Uncertain += uint64(len(batch))
			c.health.LastError = fmt.Sprintf("accounting write unacknowledged after %d retries", c.options.MaxRetries)
		}
		c.mu.Unlock()
		if initialized {
			c.persistHealth()
		}
	}
}

type accountingHealthStore interface {
	WriteAccountingHealth(context.Context, string, AccountingHealth) error
}

func (c *AccountingCollector) persistHealth() {
	store, ok := c.store.(accountingHealthStore)
	if !ok {
		return
	}
	snapshot := c.Health()
	if c.lastHealth != nil && snapshot == *c.lastHealth {
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.options.WriteTimeout)
	defer cancel()
	if err := store.WriteAccountingHealth(ctx, c.writerID, snapshot); err != nil {
		c.mu.Lock()
		c.health.WriteFailures++
		c.health.LastError = "accounting health persistence failed"
		c.mu.Unlock()
	} else {
		c.lastHealth = &snapshot
		c.mu.Lock()
		if !c.health.CoverageIncomplete && c.health.Lost == 0 && c.health.Uncertain == 0 && (c.health.LastError == "accounting health persistence failed" || c.health.LastError == "accounting initialization failed") {
			c.health.LastError = ""
		}
		c.mu.Unlock()
	}
}

// MarkCoverageIncomplete records a known coverage gap without inventing a count
// of missing attempts or tokens. It is independent of queue capacity.
func (c *AccountingCollector) MarkCoverageIncomplete(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health.CoverageIncomplete = true
	c.health.LastError = reason
}
