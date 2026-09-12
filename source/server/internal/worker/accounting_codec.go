package worker

import (
	"fmt"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	pb "google.golang.org/protobuf/proto"
)

// 64 observations × at most ~10 KiB metadata fit below this wire ceiling.
// Validate before allocating a decoded batch, even though gRPC also caps frames.
const maxAccountingWireBytes = 1 << 20

func optionalCount(c llm.TokenCount) *int64 {
	if !c.Known {
		return nil
	}
	v := c.Value
	return &v
}
func optionalMicros(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.UTC().UnixMicro()
	return &v
}
func accountingBatchToWire(id string, observations []usage.AttemptObservation) (*wire.WorkerAccountingBatch, error) {
	if id == "" || len(id) > 1024 || len(observations) == 0 || len(observations) > telemetry.MaxAccountingBatch {
		return nil, fmt.Errorf("invalid accounting batch identity or size")
	}
	out := &wire.WorkerAccountingBatch{BatchId: id, Observations: make([]*wire.AccountingAttemptObservation, 0, len(observations))}
	for _, a := range observations {
		if err := telemetry.ValidateAttempt(a); err != nil {
			return nil, err
		}
		out.Observations = append(out.Observations, &wire.AccountingAttemptObservation{
			Id: a.ID, Revision: a.Revision, OperationId: a.Attribution.OperationID, ConversationId: a.Attribution.ConversationID, SessionId: a.Attribution.SessionID, WorkerId: a.Attribution.WorkerID, Source: a.Attribution.Source,
			Provider: a.Provider, Model: a.Model, Profile: a.Profile, Destination: a.Destination, StartedAtUtcMicros: optionalMicros(a.StartedAt), EndedAtUtcMicros: optionalMicros(a.EndedAt), Outcome: string(a.Outcome),
			InputTokens: optionalCount(a.Tokens.Input), OutputTokens: optionalCount(a.Tokens.Output), CacheReadTokens: optionalCount(a.Tokens.CacheRead), CacheWriteTokens: optionalCount(a.Tokens.CacheWrite), ReasoningTokens: optionalCount(a.Tokens.Reasoning),
		})
	}
	if pb.Size(out) > maxAccountingWireBytes {
		return nil, fmt.Errorf("accounting batch exceeds wire limit")
	}
	return out, nil
}
func accountingBatchFromWire(batch *wire.WorkerAccountingBatch) ([]usage.AttemptObservation, error) {
	if batch == nil || batch.BatchId == "" || len(batch.BatchId) > 1024 || len(batch.Observations) == 0 || len(batch.Observations) > telemetry.MaxAccountingBatch {
		return nil, fmt.Errorf("invalid accounting batch identity or size")
	}
	if pb.Size(batch) > maxAccountingWireBytes {
		return nil, fmt.Errorf("accounting batch exceeds wire limit")
	}
	out := make([]usage.AttemptObservation, 0, len(batch.Observations))
	for _, v := range batch.Observations {
		if v == nil || v.StartedAtUtcMicros == nil {
			return nil, fmt.Errorf("accounting observation missing start")
		}
		a := usage.AttemptObservation{ID: v.Id, Revision: v.Revision, Attribution: usage.Attribution{OperationID: v.OperationId, ConversationID: v.ConversationId, SessionID: v.SessionId, WorkerID: v.WorkerId, Source: v.Source}, Provider: v.Provider, Model: v.Model, Profile: v.Profile, Destination: v.Destination, StartedAt: time.UnixMicro(*v.StartedAtUtcMicros).UTC(), Outcome: usage.Outcome(v.Outcome)}
		if v.EndedAtUtcMicros != nil {
			a.EndedAt = time.UnixMicro(*v.EndedAtUtcMicros).UTC()
		}
		targets := []*llm.TokenCount{&a.Tokens.Input, &a.Tokens.Output, &a.Tokens.CacheRead, &a.Tokens.CacheWrite, &a.Tokens.Reasoning}
		for i, value := range []*int64{v.InputTokens, v.OutputTokens, v.CacheReadTokens, v.CacheWriteTokens, v.ReasoningTokens} {
			if value != nil {
				if *value < 0 {
					return nil, fmt.Errorf("negative accounting token count")
				}
				*targets[i] = llm.ReportedTokens(*value)
			}
		}
		if err := telemetry.ValidateAttempt(a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
