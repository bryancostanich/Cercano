package acquisition

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"
)

func TestAcquirePersistentTimestampRollbackRefused(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()
	opts := fixtureOptions(fx, t.TempDir(), t.TempDir())
	fx.timestampOverride.Store(fx.makeTimestamp(2))
	receipt, err := acquire(context.Background(), opts, regressTarget, fx.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(receipt.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	names := outputEntries(t, opts.OutputDir)
	// New updater instance, same persistent cache, correctly signed older role.
	fx.timestampOverride.Store(fx.makeTimestamp(1))
	if r, err := acquire(context.Background(), opts, regressTarget, fx.server.Client()); err == nil || r != nil {
		t.Fatalf("rollback accepted: %+v %v", r, err)
	}
	after, err := os.ReadFile(receipt.OutputPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("prior verified output changed", err)
	}
	if !reflect.DeepEqual(names, outputEntries(t, opts.OutputDir)) {
		t.Fatal("rollback published output")
	}
}
