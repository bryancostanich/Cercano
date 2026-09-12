package telemetry

import (
	"context"
	"path/filepath"
	"testing"
)

// Reproduces the lifetime pattern in cmd/cercano.startGRPCServer: telemetry is
// deferred at startup scope although the server lives beyond startup's return.
func TestLegacyAccountingStartupLifetimeProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	start := func() *Collector {
		s, err := NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		c := NewCollector(s, 1)
		c.SetSessionID("probe-session")
		defer c.Close()
		defer s.Close()
		return c
	}
	c := start()
	c.Emit(NewEvent("after-startup", "fake"))
	if !c.closed {
		t.Fatal("expected bootstrap-scope defer to close collector")
	}
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var sessions, events int
	if err = s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	t.Logf("after startup: sessions=%d events=%d collector_closed=%v", sessions, events, c.closed)
	if sessions != 1 || events != 0 {
		t.Fatalf("unexpected observed counts: sessions=%d events=%d", sessions, events)
	}
}
