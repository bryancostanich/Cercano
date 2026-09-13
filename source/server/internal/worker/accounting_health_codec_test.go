package worker

import (
	"reflect"
	"testing"
	"time"

	"cercano/source/server/internal/telemetry"
	wire "cercano/source/server/pkg/proto"
	pb "google.golang.org/protobuf/proto"
)

func TestAccountingHealthWireRoundTrip(t *testing.T) {
	original := telemetry.AccountingHealth{Sequence: 5, Accepted: 11, Persisted: 7, Lost: 3, Pending: 2, Retries: 9, WriteFailures: 8, Uncertain: 2, OldestPending: time.Unix(123, 0).UTC(), LastPersistence: time.Unix(456, 0).UTC(), LastError: "storage unavailable", Closed: true, CoverageIncomplete: true}
	batch, err := accountingHealthToWire("batch", "writer", original)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pb.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded wire.WorkerAccountingBatch
	if err = pb.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	id, health, err := accountingHealthFromWire(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if id != "writer" || !reflect.DeepEqual(original, health) {
		t.Fatalf("health round trip: %q %+v", id, health)
	}
	if _, err = accountingBatchFromWire(&decoded); err == nil {
		t.Fatal("health accepted as an attempt batch")
	}
	decoded.Observations = []*wire.AccountingAttemptObservation{{}}
	if _, _, err = accountingHealthFromWire(&decoded); err == nil {
		t.Fatal("mixed envelope accepted")
	}
}
func TestAccountingHealthRPC(t *testing.T) {
	worker, client := accountingTestConnection(t)
	stream, _ := openAccountingStream(t, client)
	result := make(chan error, 1)
	go func() {
		result <- worker.accountingWriter().WriteAccountingHealth(t.Context(), "writer", telemetry.AccountingHealth{Lost: 4, CoverageIncomplete: true})
	}()
	batch, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	writer, h, err := accountingHealthFromWire(batch)
	if err != nil {
		t.Fatal(err)
	}
	if writer != "writer" || h.Sequence != 1 || h.Lost != 4 || !h.CoverageIncomplete {
		t.Fatalf("health=%+v", h)
	}
	if err = stream.Send(&wire.WorkerAccountingReceipt{BatchId: batch.BatchId, Persisted: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("health receipt ignored")
	}
}
