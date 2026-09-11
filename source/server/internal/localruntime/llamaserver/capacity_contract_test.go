package llamaserver

import (
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServingCapacityContracts(t *testing.T) {
	for _, tc := range []struct {
		name, props, slots string
		want               int
	}{
		{"unified", `{"default_generation_settings":{"n_ctx":131072},"total_slots":4}`, `[{"n_ctx":131072},{"n_ctx":131072},{"n_ctx":131072},{"n_ctx":131072}]`, 131072},
		{"single", `{"default_generation_settings":{"n_ctx":8192},"total_slots":1}`, `[{"n_ctx":8192}]`, 8192},
		{"missing", `{}`, `[]`, 0},
		{"contradictory", `{"default_generation_settings":{"n_ctx":65536},"total_slots":1}`, `[{"n_ctx":8192}]`, 0},
		{"slot_count", `{"default_generation_settings":{"n_ctx":8192},"total_slots":2}`, `[{"n_ctx":8192}]`, 0},
		{"malformed", `invalid`, `[]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/props" {
					w.Write([]byte(tc.props))
				} else {
					w.Write([]byte(tc.slots))
				}
			}))
			defer srv.Close()
			p := NewProvider(config.LlamaServerConfig{})
			n, err := p.readServingCapacity(context.Background(), srv.URL)
			if tc.want == 0 {
				if err == nil {
					t.Fatal("invalid evidence accepted")
				}
			} else if err != nil || n != tc.want {
				t.Fatalf("got %d,%v want %d", n, err, tc.want)
			}
		})
	}
}

func TestFailedConfirmationInvalidatesPreviousCapacity(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	p := NewProvider(config.LlamaServerConfig{})
	p.running["fixture"] = &managedInstance{record: localruntime.InstanceRecord{ID: "fixture", State: localruntime.InstanceRunning, Endpoint: srv.URL, Context: localruntime.ContextCapacity{ConfirmedTokens: 8192, ConfirmedAt: time.Now()}}}
	if err := p.confirmCapacity(context.Background(), "fixture", srv.URL); err == nil {
		t.Fatal("expected confirmation failure")
	}
	if p.running["fixture"].record.Context.ConfirmedTokens != 0 {
		t.Fatal("failed confirmation retained old authoritative capacity")
	}
}

func TestConfirmationRejectsReplacedGeneration(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	old := &managedInstance{record: localruntime.InstanceRecord{ID: "fixture", PID: 1, State: localruntime.InstanceStarting, StartedAt: time.Now()}}
	p.running["fixture"] = old
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			p.mu.Lock()
			p.running["fixture"] = &managedInstance{record: localruntime.InstanceRecord{ID: "fixture", PID: 2, State: localruntime.InstanceStarting}}
			p.mu.Unlock()
		}
		capacityFixture(w, r)
	}))
	defer srv.Close()
	old.record.Endpoint = srv.URL
	if err := p.confirmCapacity(context.Background(), "fixture", srv.URL); err == nil {
		t.Fatal("stale generation accepted")
	}
	if p.running["fixture"].record.Context.ConfirmedTokens != 0 {
		t.Fatal("replacement inherited capacity")
	}
}
