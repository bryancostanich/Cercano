package builtins

import (
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"context"
	"strings"
	"testing"
)

func TestGetSharedSkillUsesPinnedAssignment(t *testing.T) {
	ctx := managedsettings.WithSnapshot(context.Background(), settingstest.Snapshot("company-a", "7", "Review database migrations."))
	cap := GetSharedSkill()
	result, err := cap.Execute(ctx, &capabilities.Call{Args: []byte(`{"id":"enterprise/company-a/review"}`)})
	if err != nil || !strings.Contains(result.Text, "Review database migrations.") || !strings.Contains(result.Text, `"Version":"7"`) || cap.Tier() != capabilities.TierR {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, args := range []string{`{"id":"review"}`, `{"id":"enterprise/company-b/review"}`, `{"id":"../review"}`} {
		if _, err := cap.Execute(ctx, &capabilities.Call{Args: []byte(args)}); err == nil {
			t.Fatal("unassigned skill returned")
		}
	}
	if _, err := cap.Execute(context.Background(), &capabilities.Call{Args: []byte(`{"id":"enterprise/company-a/review"}`)}); err == nil {
		t.Fatal("skill leaked into standalone")
	}
}
