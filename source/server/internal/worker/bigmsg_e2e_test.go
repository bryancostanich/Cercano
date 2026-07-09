package worker_test

// bigmsg_e2e_test.go — regression guard for the worker gRPC message-size limit.
//
// The host client dials with 64 MiB call limits (worker.MaxMsgBytes) but the
// worker's gRPC server previously used grpc.NewServer() with gRPC's 4 MiB recv
// default. A StartTurn carrying assembled history plus inline images easily
// exceeds 4 MiB, so it was rejected with ResourceExhausted at the transport
// layer — a failure in-process mode never hits. This drives a real worker with a
// ~5 MiB StartTurn and asserts the worker server accepts it (no ResourceExhausted).

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

func TestWorker_RealProcess_AcceptsLargeStartTurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-process large-message e2e under -short")
	}
	bin := buildCercanoBinary(t)
	proc, dial := spawnRealWorker(t, bin)
	defer func() { _ = proc.Process.Kill() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := dial(ctx)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// A ~5 MiB inline image — over gRPC's 4 MiB default, under our 64 MiB limit.
	bigImage := &proto.InlineImage{
		Index:     0,
		Data:      make([]byte, 5<<20),
		MediaType: "image/png",
	}
	start := &proto.StartTurn{
		ConversationId: "big-msg",
		Input:          "describe this",
		Images:         []*proto.InlineImage{bigImage},
		WorkDir:        t.TempDir(),
		Gen:            1,
		Config:         worker.SnapshotConfig(config.Config{LocusMode: "open_only"}, ""),
		PermissionMode: "permissive",
	}

	stream, err := proto.NewWorkerClient(conn).RunTurn(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: start}}); err != nil {
		t.Fatalf("send StartTurn: %v", err)
	}

	// Drain the stream. The turn will error (open_only with no reachable model),
	// but that error must NOT be ResourceExhausted from the message size — the
	// server must have ACCEPTED the 5 MiB StartTurn.
	for {
		_, recvErr := stream.Recv()
		if recvErr == nil {
			continue // an event/persist/done — keep draining.
		}
		if errors.Is(recvErr, io.EOF) {
			break // clean end.
		}
		if st, ok := status.FromError(recvErr); ok && st.Code() == codes.ResourceExhausted {
			t.Fatalf("worker rejected the 5 MiB StartTurn with ResourceExhausted — "+
				"the worker gRPC server must set MaxRecvMsgSize to worker.MaxMsgBytes: %v", recvErr)
		}
		break // any other error is fine — the size limit did its job.
	}
}
