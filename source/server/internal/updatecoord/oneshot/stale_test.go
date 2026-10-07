package oneshot

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/updatecoord/operation"
)

func TestLateFailureDoesNotMarkReplacementOperationFailed(t *testing.T) {
	s, a := openStore(t)
	old := mustStart(t, a, "1.0.0")
	e, err := New(s, func(ctx context.Context, c *Controller) error {
		if _, err := c.Apply(ctx, operation.Input{Event: operation.EventCancel}); err != nil {
			return err
		}
		if _, err := a.Start(ctx, "2.0.0"); err != nil {
			return err
		}
		if _, _, err := c.Snapshot(ctx); !errors.Is(err, ErrStaleOperation) {
			t.Errorf("bound controller returned replacement snapshot: %v", err)
		}
		return errors.New("late old-operation failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Run(context.Background(), Request{InstallID: testInstallID, OperationID: old.ID})
	if !errors.Is(err, ErrStaleOperation) {
		t.Errorf("expected stale outcome refusal: %v", err)
	}
	current := currentSnapshot(t, a)
	if current.TargetVersion != "2.0.0" || current.State != operation.StateChecking || current.HasFailure {
		t.Fatalf("old executor modified replacement: %+v", current)
	}
}
