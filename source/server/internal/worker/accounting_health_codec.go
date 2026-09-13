package worker

import (
	"fmt"
	"time"

	"cercano/source/server/internal/telemetry"
	wire "cercano/source/server/pkg/proto"
	pb "google.golang.org/protobuf/proto"
)

func accountingHealthToWire(batchID, writer string, h telemetry.AccountingHealth) (*wire.WorkerAccountingBatch, error) {
	if batchID == "" || len(batchID) > 1024 || len(writer) > 256 {
		return nil, fmt.Errorf("invalid accounting health identity")
	}
	if err := telemetry.ValidateRemoteHealth(writer, h); err != nil {
		return nil, err
	}
	out := &wire.WorkerAccountingBatch{BatchId: batchID, Health: &wire.WorkerAccountingHealth{
		WriterId: writer, Sequence: h.Sequence, Accepted: h.Accepted, Persisted: h.Persisted, Lost: h.Lost, Pending: uint32(h.Pending), Retries: h.Retries, WriteFailures: h.WriteFailures, Uncertain: h.Uncertain, OldestPendingUtcMicros: optionalMicros(h.OldestPending), LastPersistenceUtcMicros: optionalMicros(h.LastPersistence), LastError: h.LastError, Closed: h.Closed, CoverageIncomplete: h.CoverageIncomplete,
	}}
	if pb.Size(out) > maxAccountingWireBytes {
		return nil, fmt.Errorf("accounting health exceeds wire limit")
	}
	return out, nil
}
func accountingHealthFromWire(batch *wire.WorkerAccountingBatch) (string, telemetry.AccountingHealth, error) {
	if batch == nil || batch.BatchId == "" || len(batch.BatchId) > 1024 || batch.Health == nil || batch.DrainFinished || batch.DrainError != "" || len(batch.Observations) != 0 || pb.Size(batch) > maxAccountingWireBytes {
		return "", telemetry.AccountingHealth{}, fmt.Errorf("invalid accounting health envelope")
	}
	v := batch.Health
	if len(v.WriterId) > 256 || v.Pending > 4096 {
		return "", telemetry.AccountingHealth{}, fmt.Errorf("invalid accounting health identity or pending count")
	}
	h := telemetry.AccountingHealth{Sequence: v.Sequence, Accepted: v.Accepted, Persisted: v.Persisted, Lost: v.Lost, Pending: int(v.Pending), Retries: v.Retries, WriteFailures: v.WriteFailures, Uncertain: v.Uncertain, LastError: v.LastError, Closed: v.Closed, CoverageIncomplete: v.CoverageIncomplete}
	if v.OldestPendingUtcMicros != nil {
		h.OldestPending = time.UnixMicro(*v.OldestPendingUtcMicros).UTC()
	}
	if v.LastPersistenceUtcMicros != nil {
		h.LastPersistence = time.UnixMicro(*v.LastPersistenceUtcMicros).UTC()
	}
	if err := telemetry.ValidateRemoteHealth(v.WriterId, h); err != nil {
		return "", telemetry.AccountingHealth{}, err
	}
	return v.WriterId, h, nil
}
