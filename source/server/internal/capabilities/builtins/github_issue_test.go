package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestGitHubIssueCloseCap_Meta(t *testing.T) {
	cap := GitHubIssueClose()
	if cap.Name() != "github_issue_close" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierX {
		t.Fatalf("tier wrong: %q", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
	if !strings.Contains(cap.Description(), "Close a GitHub issue") {
		t.Fatalf("description should explain the scoped GitHub action: %q", cap.Description())
	}
}

func TestGitHubIssueCloseCap_RequiresPositiveIssueNumber(t *testing.T) {
	cap := GitHubIssueClose()
	args, _ := json.Marshal(map[string]any{"number": 0})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil || !strings.Contains(err.Error(), "number must be positive") {
		t.Fatalf("err = %v, want positive-number validation", err)
	}
}
