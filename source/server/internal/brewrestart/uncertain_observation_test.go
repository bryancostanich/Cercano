package brewrestart

import (
	"context"
	"syscall"
	"testing"
	"time"
)

type uncertainObservationSource struct {
	processSource
	original Identity
	calls    int
}

func (s *uncertainObservationSource) Inspect(int) (Identity, error) {
	s.calls++
	if s.calls == 1 {
		return s.original, nil
	}
	return Identity{}, syscall.EAGAIN
}
func TestSafeStopEarlierAliveSampleDoesNotOverrideLaterUncertainty(t *testing.T) {
	source := &uncertainObservationSource{original: Identity{PID: 123}}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	result, _ := confirmStopAfterUncertainty(ctx, source, source.original)
	if source.calls < 2 {
		t.Fatal("fixture never reached uncertain sample")
	}
	if result != stopUnknown {
		t.Fatalf("stale alive sample treated as current confirmation: %v", result)
	}
}
