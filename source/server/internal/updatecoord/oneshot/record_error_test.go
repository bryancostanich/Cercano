package oneshot

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailureRecordingErrorIsNotHidden(t *testing.T) {
	s, a := openStore(t)
	op := mustStart(t, a, "1.0.0")
	e, err := New(s, func(context.Context, *Controller) error {
		db, err := sql.Open("sqlite", filepath.Join(s.Directory(), "state.db"))
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = db.Exec(`CREATE TRIGGER fixture_abort BEFORE UPDATE ON operation_records BEGIN SELECT RAISE(ABORT,'fixture outcome-write failure'); END;`)
		if err != nil {
			t.Fatal(err)
		}
		return errors.New("backend failed")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Run(context.Background(), Request{InstallID: testInstallID, OperationID: op.ID})
	if !errors.Is(err, ErrBackendFailed) || !errors.Is(err, ErrOutcomeRecording) || !strings.Contains(err.Error(), "fixture outcome-write failure") {
		t.Fatalf("outcome persistence error hidden: %v", err)
	}
	if snap := currentSnapshot(t, a); snap.HasFailure {
		t.Fatal("fixture unexpectedly persisted failure")
	}
}
