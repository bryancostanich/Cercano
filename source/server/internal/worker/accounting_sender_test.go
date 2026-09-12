package worker

import (
	"testing"
	"time"

	proto "cercano/source/server/pkg/proto"
)

type accountingGateStream struct {
	proto.Worker_RunTurnServer
	entered chan struct{}
	release chan struct{}
	got     chan *proto.WorkerToHost
	calls   int
}

func (s *accountingGateStream) Send(m *proto.WorkerToHost) error {
	s.calls++
	if s.calls == 1 {
		close(s.entered)
		<-s.release
	}
	s.got <- m
	return nil
}

func TestAccountingAdmissionDoesNotBlockOrDisplaceControl(t *testing.T) {
	stream := &accountingGateStream{entered: make(chan struct{}), release: make(chan struct{}), got: make(chan *proto.WorkerToHost, 8)}
	s := newSender(stream)
	first, control, accounting := &proto.WorkerToHost{}, &proto.WorkerToHost{}, &proto.WorkerToHost{}
	s.send(first)
	<-stream.entered
	accepted := make(chan bool, 1)
	go func() { accepted <- s.trySendAccounting(accounting) }()
	select {
	case ok := <-accepted:
		if !ok {
			t.Fatal("empty accounting slot rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("accounting admission waited for transport")
	}
	if s.trySendAccounting(&proto.WorkerToHost{}) {
		t.Fatal("accounting queue must be bounded to one frame")
	}
	s.send(control)
	close(stream.release)
	s.close()
	for i, want := range []*proto.WorkerToHost{first, control, accounting} {
		if got := <-stream.got; got != want {
			t.Fatalf("send %d: accounting displaced control", i)
		}
	}
	if s.trySendAccounting(accounting) {
		t.Fatal("accepted accounting after close")
	}
}
