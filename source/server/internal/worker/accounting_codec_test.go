package worker

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	pb "google.golang.org/protobuf/proto"
)

func wireAttemptFixture() usage.AttemptObservation {
	return usage.AttemptObservation{ID: "attempt", Revision: 2, Attribution: usage.Attribution{OperationID: "operation", ConversationID: "conversation", SessionID: "session", WorkerID: "worker", Source: "main"}, Provider: "provider", Model: "model", Profile: "profile", Destination: "local", StartedAt: time.Unix(100, 123456000).UTC(), EndedAt: time.Unix(101, 654321000).UTC(), Outcome: usage.Completed, Tokens: llm.TokenUsage{Final: true, Input: llm.ReportedTokens(0), Output: llm.ReportedTokens(17), CacheRead: llm.ReportedTokens(3), CacheWrite: llm.ReportedTokens(0), ReasoningChunks: llm.ReportedTokens(0), ReasoningBytes: llm.ReportedTokens(8383)}}
}
func TestAccountingWireRoundTrip(t *testing.T) {
	original := wireAttemptFixture()
	batch, err := accountingBatchToWire("batch", []usage.AttemptObservation{original})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pb.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var received wire.WorkerAccountingBatch
	if err = pb.Unmarshal(raw, &received); err != nil {
		t.Fatal(err)
	}
	decoded, err := accountingBatchFromWire(&received)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || !reflect.DeepEqual(original, decoded[0]) {
		t.Fatalf("round trip: %+v", decoded)
	}
	if received.Observations[0].InputTokens == nil || received.Observations[0].ReasoningTokens != nil || received.Observations[0].ReasoningChunks == nil || *received.Observations[0].ReasoningChunks != 0 {
		t.Fatal("zero and unknown lost their distinction")
	}
}
func TestAccountingWireEpochAndTimezone(t *testing.T) {
	original := wireAttemptFixture()
	original.StartedAt = time.Unix(0, 0).In(time.FixedZone("viewer", -7*3600))
	original.EndedAt = time.Time{}
	original.Outcome = usage.Started
	batch, err := accountingBatchToWire("batch", []usage.AttemptObservation{original})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Observations[0].StartedAtUtcMicros == nil || *batch.Observations[0].StartedAtUtcMicros != 0 || batch.Observations[0].EndedAtUtcMicros != nil {
		t.Fatal("timestamp presence lost")
	}
	decoded, err := accountingBatchFromWire(batch)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded[0].StartedAt.Equal(original.StartedAt) || decoded[0].StartedAt.Location() != time.UTC || !decoded[0].EndedAt.IsZero() {
		t.Fatal("timestamps were not normalized")
	}
}
func TestAccountingWireRejectsMalformedBatches(t *testing.T) {
	for _, mutate := range []func(*wire.WorkerAccountingBatch){
		func(b *wire.WorkerAccountingBatch) { b.BatchId = "" },
		func(b *wire.WorkerAccountingBatch) { b.Observations = nil },
		func(b *wire.WorkerAccountingBatch) { b.Observations = make([]*wire.AccountingAttemptObservation, 65) },
		func(b *wire.WorkerAccountingBatch) { b.Observations[0] = nil },
		func(b *wire.WorkerAccountingBatch) { b.Observations[0].StartedAtUtcMicros = nil },
		func(b *wire.WorkerAccountingBatch) { b.Observations[0].Source = strings.Repeat("x", 1025) },
		func(b *wire.WorkerAccountingBatch) { n := int64(-1); b.Observations[0].InputTokens = &n },
		func(b *wire.WorkerAccountingBatch) { b.Observations = append(b.Observations, nil) },
	} {
		batch, err := accountingBatchToWire("batch", []usage.AttemptObservation{wireAttemptFixture()})
		if err != nil {
			t.Fatal(err)
		}
		mutate(batch)
		if got, err := accountingBatchFromWire(batch); err == nil || got != nil {
			t.Fatalf("malformed batch partially accepted: %+v %v", got, err)
		}
	}
}
