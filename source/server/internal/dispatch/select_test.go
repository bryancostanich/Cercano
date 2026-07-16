package dispatch

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

type stubLLM struct{ n string }

func (s stubLLM) Name() string                       { return s.n }
func (stubLLM) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (stubLLM) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (stubLLM) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func TestSelectCoprocPrefersOpenUnderCloudPrimary(t *testing.T) {
	local := stubLLM{"local"}
	cloud := stubLLM{"cloud"}
	sel, err := Select(locus.CloudPrimary, RoleCoproc, Providers{Cloud: cloud, Open: local})
	if err != nil {
		t.Fatal(err)
	}
	if sel.IsCloud || sel.Provider.Name() != "local" {
		t.Fatalf("coproc under cloud_primary must run local, got %+v", sel)
	}
}

func TestSelectFallbackNotice(t *testing.T) {
	cloud := stubLLM{"cloud"}
	// local_primary, no local available -> fall back to cloud, with notice.
	sel, err := Select(locus.OpenPrimary, RoleCoproc, Providers{Cloud: cloud, Open: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.FellBack || !sel.IsCloud {
		t.Fatalf("expected cloud fallback, got %+v", sel)
	}
	if !strings.Contains(sel.Notice, "preferred co-processor tier unavailable") {
		t.Fatalf("missing fallback notice: %q", sel.Notice)
	}
}

func TestSelectNoProviderErrors(t *testing.T) {
	if _, err := Select(locus.OpenOnly, RoleCoproc, Providers{}); err == nil {
		t.Fatal("expected error when no provider is available")
	}
}

func TestSelectCloudOnlyForbidsLocal(t *testing.T) {
	local := stubLLM{"local"}
	if _, err := Select(locus.CloudOnly, RoleMain, Providers{Open: local}); err == nil {
		t.Fatal("cloud_only with only local available must error, never run local")
	}
}

func TestSelectMainFallbackNotice(t *testing.T) {
	cloud := stubLLM{"cloud"}
	// local_primary, no local available, RoleMain -> fall back to cloud, notice says "main".
	sel, err := Select(locus.OpenPrimary, RoleMain, Providers{Cloud: cloud, Open: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.FellBack || !sel.IsCloud {
		t.Fatalf("expected cloud fallback for main role, got %+v", sel)
	}
	if !strings.Contains(sel.Notice, "preferred main tier unavailable") {
		t.Fatalf("missing main fallback notice: %q", sel.Notice)
	}
}
