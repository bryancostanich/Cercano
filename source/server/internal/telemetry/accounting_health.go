package telemetry

import (
	"fmt"
	"math"
	"time"
)

// ValidateRemoteHealth bounds an untrusted wire snapshot before queue admission.
// A health snapshot is not an inference attempt and never supplies token totals.
func ValidateRemoteHealth(writer string, h AccountingHealth) error {
	if writer == "" || len(writer) > 1024 || len(h.LastError) > 1024 {
		return fmt.Errorf("invalid accounting health metadata")
	}
	if h.Sequence == 0 || h.Sequence > math.MaxInt64 || h.Pending < 0 || h.Pending > 4096 {
		return fmt.Errorf("invalid accounting health sequence or pending count")
	}
	for _, n := range []uint64{h.Accepted, h.Persisted, h.Lost, h.Retries, h.WriteFailures, h.Uncertain} {
		if n > math.MaxInt64 {
			return fmt.Errorf("accounting health counter out of range")
		}
	}
	if h.Persisted > h.Accepted {
		return fmt.Errorf("accounting persisted count exceeds admission")
	}
	for _, at := range []time.Time{h.OldestPending, h.LastPersistence} {
		if !at.IsZero() && (at.UTC().Year() < 1 || at.UTC().Year() > 9999) {
			return fmt.Errorf("accounting health timestamp out of range")
		}
	}
	return nil
}
