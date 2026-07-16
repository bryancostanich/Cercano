package server

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
)

// buildPermsServer returns a *Server with a toolRegistry containing one R-tier
// tool ("r_read") and one W-tier tool ("w_write"), ready for grantedRegistry tests.
func buildPermsServer(t *testing.T) *Server {
	t.Helper()
	rTool := stubDispatchTool{name: "r_read", perm: agenttools.PermR}
	wTool := stubDispatchTool{name: "w_write", perm: agenttools.PermW}

	reg := agenttools.NewRegistry()
	reg.MustRegister(rTool)
	reg.MustRegister(wTool)

	srv := NewServer(nil, nil, nil, nil, nil)
	srv.SetToolRegistry(reg)
	return srv
}

func hasToolNamed(reg *agenttools.Registry, name string) bool {
	_, ok := reg.Get(name)
	return ok
}

// TestGrantedRegistry_GrantsRequestedSetVerbatim pins the confirm-once design:
// W/X tools are granted exactly as requested — the dispatch call itself
// escalates to an X-tier confirm at the parent (dispatchCap.TierFor), so this
// layer no longer drops write-capable tools by permission mode. The granted
// and ignored lists ride back for caller visibility.
func TestGrantedRegistry_GrantsRequestedSetVerbatim(t *testing.T) {
	srv := buildPermsServer(t)
	reg, granted, ignored, err := srv.grantedRegistry([]string{"r_read", "w_write"})
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	if !hasToolNamed(reg, "r_read") || !hasToolNamed(reg, "w_write") {
		t.Errorf("expected both requested tools granted, got %v", granted)
	}
	if len(granted) != 2 {
		t.Errorf("granted = %v, want both names", granted)
	}
	if len(ignored) != 0 {
		t.Errorf("ignored = %v, want none", ignored)
	}
}

// TestGrantedRegistry_ReportsUnknownToolNames verifies unknown names are
// surfaced in the returned ignored list AND the log (no silent caps).
func TestGrantedRegistry_ReportsUnknownToolNames(t *testing.T) {
	srv := buildPermsServer(t)

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	_, granted, ignored, err := srv.grantedRegistry([]string{"r_read", "bogus_tool"})
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}
	if len(granted) != 1 || granted[0] != "r_read" {
		t.Errorf("granted = %v, want [r_read]", granted)
	}
	if len(ignored) != 1 || ignored[0] != "bogus_tool" {
		t.Errorf("ignored = %v, want [bogus_tool]", ignored)
	}

	out := buf.String()
	var unknownLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "ignored unknown tool names") {
			unknownLine = line
			break
		}
	}
	if unknownLine == "" {
		t.Fatalf("expected an unknown-tool-names log line, got: %q", out)
	}
	if !strings.Contains(unknownLine, "bogus_tool") {
		t.Fatalf("expected unknown tool name in %q", unknownLine)
	}
	if strings.Contains(unknownLine, "r_read") {
		t.Errorf("known tool r_read should not be reported as unknown: %q", unknownLine)
	}
}

// TestGrantedRegistry_AllUnknownReturnsError verifies that when every requested
// tool name is unknown (after prefix normalization), the sub-agent is not
// spawned with an empty catalog — the caller gets a clear error naming the
// offending inputs and the registered tools available.
func TestGrantedRegistry_AllUnknownReturnsError(t *testing.T) {
	srv := buildPermsServer(t)

	_, _, _, err := srv.grantedRegistry([]string{"totally_bogus", "also_bogus"})
	if err == nil {
		t.Fatal("expected error when all requested tools are unknown, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "totally_bogus") {
		t.Errorf("expected error to name the unknown tool, got: %q", msg)
	}
	if !strings.Contains(msg, "available tools") {
		t.Errorf("expected error to list available tools, got: %q", msg)
	}
	if !strings.Contains(msg, "r_read") || !strings.Contains(msg, "w_write") {
		t.Errorf("hint should list all registered tools (R and W), got: %q", msg)
	}
}

// TestGrantedRegistry_PrefixedNameResolvesToPlainTool verifies that when a
// caller passes a host-prefixed name like "mcp__oc__r_read" and no tool is
// registered under that exact name, grantedRegistry strips the prefix and
// finds "r_read" — so a misgranted-but-recognizable name still works.
func TestGrantedRegistry_PrefixedNameResolvesToPlainTool(t *testing.T) {
	srv := buildPermsServer(t)

	reg, _, _, err := srv.grantedRegistry([]string{"mcp__oc__r_read"})
	if err != nil {
		t.Fatalf("prefix normalization should resolve mcp__oc__r_read to r_read, got: %v", err)
	}
	if !hasToolNamed(reg, "r_read") {
		t.Errorf("expected r_read to be in the resulting registry after prefix strip")
	}
}

// TestGrantedRegistry_ExactNameWinsOverStrippedForm verifies the "exact match
// first" rule: if a tool is registered under the literal fully-qualified name
// mcp__oc__X (as happens when Cercano hosts an MCP server named "oc"), the
// grant resolves to that tool, NOT to a plain-named X that happens to exist.
func TestGrantedRegistry_ExactNameWinsOverStrippedForm(t *testing.T) {
	plain := stubDispatchTool{name: "widget", perm: agenttools.PermR}
	hosted := stubDispatchTool{name: "mcp__oc__widget", perm: agenttools.PermR}

	reg := agenttools.NewRegistry()
	reg.MustRegister(plain)
	reg.MustRegister(hosted)

	srv := NewServer(nil, nil, nil, nil, nil)
	srv.SetToolRegistry(reg)

	out, _, _, err := srv.grantedRegistry([]string{"mcp__oc__widget"})
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}
	if !hasToolNamed(out, "mcp__oc__widget") {
		t.Errorf("exact match should have resolved to the fully-qualified tool, got registry: %+v", out.All())
	}
	if hasToolNamed(out, "widget") {
		t.Errorf("exact match should NOT have fallen through to the stripped form")
	}
}

// TestGrantedRegistry_NilToolsDefaultsReadOnly verifies the least-privilege
// default: no requested tools grants the R-tier catalog only.
func TestGrantedRegistry_NilToolsDefaultsReadOnly(t *testing.T) {
	srv := buildPermsServer(t)
	reg, granted, ignored, err := srv.grantedRegistry(nil)
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	if !hasToolNamed(reg, "r_read") {
		t.Error("nil tools: expected r_read to be present")
	}
	if hasToolNamed(reg, "w_write") {
		t.Error("nil tools: default grant must stay read-only")
	}
	if len(reg.All()) != 1 || len(granted) != 1 || len(ignored) != 0 {
		t.Errorf("nil tools: expected exactly the R catalog, got granted=%v ignored=%v", granted, ignored)
	}
}
