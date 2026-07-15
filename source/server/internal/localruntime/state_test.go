package localruntime

import "testing"

// TestDownloadStateStringsRoundTrip locks the wire strings — the gRPC surface
// and every persisted seed depend on these exact values.
func TestDownloadStateStringsRoundTrip(t *testing.T) {
	cases := map[DownloadState]string{
		DownloadNotStarted: "not_downloaded",
		Downloading:        "downloading",
		Downloaded:         "downloaded",
		DownloadFailed:     "failed",
		DownloadCancelled:  "cancelled",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", state, got, want)
		}
		parsed, ok := ParseDownloadState(want)
		if !ok || parsed != state {
			t.Errorf("ParseDownloadState(%q) = %v,%v, want %v,true", want, parsed, ok, state)
		}
	}
	if _, ok := ParseDownloadState("bogus"); ok {
		t.Error("ParseDownloadState(bogus) reported ok=true")
	}
}

func TestInstanceStateStringsRoundTrip(t *testing.T) {
	cases := map[InstanceState]string{
		InstanceUnknown:     "unknown",
		InstanceStarting:    "starting",
		InstanceRunning:     "running",
		InstanceHealthy:     "healthy",
		InstanceDegraded:    "degraded",
		InstanceUnhealthy:   "unhealthy",
		InstanceUnreachable: "unreachable",
		InstanceStopped:     "stopped",
		InstanceCrashed:     "crashed",
		InstanceFailed:      "failed",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", state, got, want)
		}
		parsed, ok := ParseInstanceState(want)
		if !ok || parsed != state {
			t.Errorf("ParseInstanceState(%q) = %v,%v, want %v,true", want, parsed, ok, state)
		}
	}
}

// TestDownloadTransitions asserts the legal and illegal moves that matter for
// correctness: the happy path, retry-after-failure, delete, and the forbidden
// jumps (e.g. skipping straight from not-started to downloaded).
func TestDownloadTransitions(t *testing.T) {
	legal := [][2]DownloadState{
		{DownloadNotStarted, Downloading},
		{Downloading, Downloaded},
		{Downloading, DownloadFailed},
		{Downloading, DownloadCancelled},
		{Downloaded, DownloadNotStarted},  // delete
		{DownloadFailed, Downloading},     // retry
		{DownloadCancelled, Downloading},  // retry
		{Downloading, Downloading},        // idempotent progress tick
	}
	for _, mv := range legal {
		if !mv[0].CanTransitionTo(mv[1]) {
			t.Errorf("expected legal: %s → %s", mv[0], mv[1])
		}
	}
	illegal := [][2]DownloadState{
		{DownloadNotStarted, Downloaded}, // must go through Downloading
		{DownloadNotStarted, DownloadFailed},
		{Downloaded, Downloading}, // re-download must clear first
		{Downloaded, DownloadFailed},
	}
	for _, mv := range illegal {
		if mv[0].CanTransitionTo(mv[1]) {
			t.Errorf("expected illegal: %s → %s", mv[0], mv[1])
		}
	}
}

func TestInstanceTransitions(t *testing.T) {
	legal := [][2]InstanceState{
		{InstanceUnknown, InstanceStarting},
		{InstanceStarting, InstanceRunning},
		{InstanceRunning, InstanceHealthy},
		{InstanceHealthy, InstanceUnhealthy},
		{InstanceHealthy, InstanceStopped},
		{InstanceStopped, InstanceStarting}, // relaunch
		{InstanceCrashed, InstanceStarting}, // restart
		{InstanceFailed, InstanceStopped},   // give up
		{InstanceRunning, InstanceRunning},  // idempotent
	}
	for _, mv := range legal {
		if !mv[0].CanTransitionTo(mv[1]) {
			t.Errorf("expected legal: %s → %s", mv[0], mv[1])
		}
	}
	illegal := [][2]InstanceState{
		{InstanceStopped, InstanceHealthy},  // must start first
		{InstanceStopped, InstanceRunning},  // must start first
		{InstanceFailed, InstanceHealthy},   // must relaunch first
	}
	for _, mv := range illegal {
		if mv[0].CanTransitionTo(mv[1]) {
			t.Errorf("expected illegal: %s → %s", mv[0], mv[1])
		}
	}
}

// recordingObserver captures events for assertions.
type recordingObserver struct {
	downloads []DownloadEvent
	instances []InstanceEvent
}

func (r *recordingObserver) OnDownloadStateChange(ev DownloadEvent) {
	r.downloads = append(r.downloads, ev)
}
func (r *recordingObserver) OnInstanceStateChange(ev InstanceEvent) {
	r.instances = append(r.instances, ev)
}

// TestSetDownloadStateGuardsAndNotifies verifies the guarded funnel fires
// observers on a legal move and refuses (without notifying) an illegal one.
func TestSetDownloadStateGuardsAndNotifies(t *testing.T) {
	m := NewManager()
	obs := &recordingObserver{}
	m.RegisterObserver(obs)

	model := ModelRecord{ID: "m1", DisplayName: "M1", DownloadState: DownloadNotStarted}
	m.storeDownload(model)

	// Legal: not-started → downloading.
	m.setDownloadState(model, Downloading, "")
	if len(obs.downloads) != 1 || obs.downloads[0].Next != Downloading {
		t.Fatalf("expected one Downloading event, got %+v", obs.downloads)
	}

	// Illegal: downloading → not-started is not in the table; the funnel must
	// refuse and NOT emit an event.
	m.setDownloadState(model, Downloaded, "") // legal, advance to Downloaded
	before := len(obs.downloads)
	m.setDownloadState(model, Downloading, "") // Downloaded → Downloading is illegal
	if len(obs.downloads) != before {
		t.Errorf("illegal transition emitted an event: %+v", obs.downloads[before:])
	}
	// State must remain Downloaded after the refused move.
	if got, _ := m.download("m1"); got.DownloadState != Downloaded {
		t.Errorf("state corrupted by illegal move: %s", got.DownloadState)
	}
}

// TestUpdateInstanceGuardsAndNotifies verifies the instance funnel emits on a
// real change, stays quiet on a no-op, and refuses an illegal jump.
func TestUpdateInstanceGuardsAndNotifies(t *testing.T) {
	m := NewManager()
	obs := &recordingObserver{}
	m.RegisterObserver(obs)

	inst := InstanceRecord{ID: "i1", State: InstanceStarting}
	m.UpdateInstance(inst) // unknown → starting (legal)
	inst.State = InstanceRunning
	m.UpdateInstance(inst) // starting → running (legal)
	if len(obs.instances) != 2 {
		t.Fatalf("expected 2 instance events, got %d: %+v", len(obs.instances), obs.instances)
	}

	// No-op: re-store running; no event.
	m.UpdateInstance(inst)
	if len(obs.instances) != 2 {
		t.Errorf("no-op emitted an event: %+v", obs.instances[2:])
	}

	// Illegal: running → starting is not allowed; refuse and keep running.
	inst.State = InstanceStarting
	m.UpdateInstance(inst)
	if len(obs.instances) != 2 {
		t.Errorf("illegal instance move emitted an event")
	}
	insts, _ := m.Instances(nil)
	if len(insts) != 1 || insts[0].State != InstanceRunning {
		t.Errorf("illegal move corrupted instance state: %+v", insts)
	}
}
