package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/pkg/config"
)

type stubProv struct{ name string }

func (s stubProv) Name() string                { return s.name }
func (s stubProv) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (s stubProv) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	panic("unused")
}
func (s stubProv) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	panic("unused")
}

func TestResolveMainProvider(t *testing.T) {
	cloud := stubProv{"anthropic"}
	local := stubProv{"ollama"}

	mk := func(mode string, c, l llm.Provider) *Server {
		s := &Server{cfgSvc: cfgsvc.New("", config.Config{LocusMode: mode}, nil)}
		s.cloudLLMProvider = c
		s.openLLMProvider = l
		return s
	}

	// Local Primary, both available → local, not fallback.
	if p, isCloud, fb, err := mk("open_primary", cloud, local).resolveMainProvider(); err != nil || isCloud || fb || p.Name() != "ollama" {
		t.Errorf("local_primary both: %v isCloud=%v fb=%v err=%v", p, isCloud, fb, err)
	}
	// Local Primary, local missing → fall back to cloud.
	if p, isCloud, fb, err := mk("open_primary", cloud, nil).resolveMainProvider(); err != nil || !isCloud || !fb || p.Name() != "anthropic" {
		t.Errorf("local_primary fallback: %v isCloud=%v fb=%v err=%v", p, isCloud, fb, err)
	}
	// Cloud Primary, cloud missing → fall back to local (mirror of the above).
	if p, isCloud, fb, err := mk("cloud_primary", nil, local).resolveMainProvider(); err != nil || isCloud || !fb || p.Name() != "ollama" {
		t.Errorf("cloud_primary fallback: %v isCloud=%v fb=%v err=%v", p, isCloud, fb, err)
	}
	// Cloud Only, cloud missing → hard error (no cross).
	if _, _, _, err := mk("cloud_only", nil, local).resolveMainProvider(); err == nil {
		t.Error("cloud_only with no cloud should error")
	}
	// Local Only, local missing → hard error (no cross even though cloud present).
	if _, _, _, err := mk("open_only", cloud, nil).resolveMainProvider(); err == nil {
		t.Error("local_only with no local should error")
	}
}
