package telemetry

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Hold the first observed real SQLite lock failure so recovery ordering is
// deterministic rather than relying on scheduler timing or arbitrary sleeps.
type lockedAccountingStore struct {
	*SQLiteStore
	failed  chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (s *lockedAccountingStore) InitializeAccounting(ctx context.Context) error {
	err := s.SQLiteStore.InitializeAccounting(ctx)
	if err != nil {
		s.once.Do(func() {
			close(s.failed)
			select {
			case <-s.proceed:
			case <-ctx.Done():
			}
		})
	}
	return err
}
func TestAccountingDatabaseLockRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	primary, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	if err = primary.InitializeAccounting(t.Context()); err != nil {
		t.Fatal(err)
	}
	blocker, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	if _, err = blocker.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer blocker.db.Exec(`ROLLBACK`)
	gated := &lockedAccountingStore{SQLiteStore: primary, failed: make(chan struct{}), proceed: make(chan struct{})}
	c := NewAccountingCollector(gated, testAccountingOptions())
	if !c.Emit(accountingFixture("locked")) {
		t.Fatal("admission failed while DB locked")
	}
	select {
	case <-gated.failed:
	case <-time.After(3 * time.Second):
		t.Fatal("expected SQLite lock error")
	}
	if _, err = blocker.db.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	close(gated.proceed)
	drainAccounting(t, c)
	h := c.Health()
	if h.Persisted != 1 || h.WriteFailures == 0 || h.Retries == 0 || h.Uncertain != 0 {
		t.Fatalf("lock recovery=%+v", h)
	}
	if _, err = primary.AccountingAttempt(t.Context(), "locked"); err != nil {
		t.Fatal(err)
	}
}
func TestAccountingRestartPreservesIncompleteAndHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c := NewAccountingCollector(s, testAccountingOptions())
	c.Emit(accountingFixture("unfinished"))
	drainAccounting(t, c)
	since, err := s.TrackingSince(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.InitializeAccounting(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.TrackingSince(t.Context())
	if err != nil || !after.Equal(since) {
		t.Fatalf("cutover reset: %v %v", after, err)
	}
	a, err := reopened.AccountingAttempt(t.Context(), "unfinished")
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome != "started" || !a.EndedAt.IsZero() || a.Tokens.TotalsKnown() {
		t.Fatalf("restart invented a final response: %+v", a)
	}
	var healthRows int
	if err = reopened.db.QueryRow(`SELECT COUNT(*) FROM accounting_health`).Scan(&healthRows); err != nil || healthRows != 1 {
		t.Fatalf("health lost on restart: %d %v", healthRows, err)
	}
}
