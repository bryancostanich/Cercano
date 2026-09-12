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

func TestHealthAloneDoesNotConfirmServingCapacity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := NewProvider(config.LlamaServerConfig{})
	p.running["fixture"] = &managedInstance{record: localruntime.InstanceRecord{ID: "fixture", State: localruntime.InstanceStarting, Endpoint: srv.URL}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.waitReady(ctx, "fixture", srv.URL); err == nil {
		t.Fatal("health alone authorized readiness without capacity confirmation")
	}
}

func capacityFixture(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/props":
		w.Write([]byte(`{"default_generation_settings":{"n_ctx":131072},"total_slots":4}`))
		return true
	case "/slots":
		w.Write([]byte(`[{"n_ctx":131072},{"n_ctx":131072},{"n_ctx":131072},{"n_ctx":131072}]`))
		return true
	}
	return false
}
