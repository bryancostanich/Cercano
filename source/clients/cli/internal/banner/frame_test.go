package banner

import (
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

// TestFrameAt_WaitPhaseIsPlain checks that FrameAt renders the plain banner
// during the wait portion of the cycle — epoch placed mid-wait, with 500ms of
// margin before the phase window moves.
func TestFrameAt_WaitPhaseIsPlain(t *testing.T) {
	p := theme.Cracker()
	m := Meta{Tagline: "t", Version: "v", Model: "m"}
	epoch := time.Now().Add(-(SweepDuration + WaitDuration/2))
	if FrameAt(p, m, epoch) != Render(p, m) {
		t.Errorf("FrameAt in the wait phase should equal the plain Render")
	}
}

// TestFrameAt_SweepPhaseDiffers checks that mid-sweep the frame carries the
// shimmer band — i.e. differs from the plain render. Epoch placed mid-sweep,
// 700ms of margin on both sides.
func TestFrameAt_SweepPhaseDiffers(t *testing.T) {
	p := theme.Cracker()
	m := Meta{Tagline: "t", Version: "v", Model: "m"}
	epoch := time.Now().Add(-SweepDuration / 2)
	if FrameAt(p, m, epoch) == Render(p, m) {
		t.Errorf("FrameAt mid-sweep should differ from the plain Render")
	}
}
