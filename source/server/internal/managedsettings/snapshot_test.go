package managedsettings_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
)

func TestSnapshotIsolationAndRoundTrip(t *testing.T) {
	original := settingstest.Snapshot("company-a", "1", "Review carefully.")
	ctx := managedsettings.WithSnapshot(context.Background(), original)
	original.Skills[0].Content = "changed by caller"
	original.Policy.TaskDefaults[0].RouteID = "unapproved"
	got, _ := managedsettings.FromContext(ctx)
	got.Skills[0].Content = "changed by reader"
	got.Policy.TaskDefaults[0].FallbackRouteIDs = append(got.Policy.TaskDefaults[0].FallbackRouteIDs, "unapproved")
	data, err := managedsettings.Encode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := managedsettings.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Skills[0].Content != "Review carefully." || decoded.Policy.TaskDefaults[0].RouteID != "approved" || len(decoded.Policy.TaskDefaults[0].FallbackRouteIDs) != 0 {
		t.Fatal("snapshot mutated")
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "api_key"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("credentials in snapshot")
		}
	}
	if _, ok := managedsettings.Skill(ctx, "enterprise/company-b/review"); ok {
		t.Fatal("other customer's skill exposed")
	}
	if _, ok := managedsettings.Skill(context.Background(), "enterprise/company-a/review"); ok {
		t.Fatal("standalone inherited managed skill")
	}
	if managedsettings.SkillPrompt(context.Background()) != "" {
		t.Fatal("standalone prompt changed")
	}
}
func TestRejectIncompleteOrAlteredBundle(t *testing.T) {
	for _, mutation := range []string{"missing", "duplicate", "content", "version", "route"} {
		t.Run(mutation, func(t *testing.T) {
			s := settingstest.Snapshot("company-a", "1", "Review carefully.")
			switch mutation {
			case "missing":
				s.Skills = nil
			case "duplicate":
				s.Skills = append(s.Skills, s.Skills[0])
			case "content":
				s.Skills[0].Content = "tampered"
			case "version":
				s.Skills[0].Version = "2"
			case "route":
				s.Policy.TaskDefaults[0].RouteID = "unapproved"
			}
			data, _ := json.Marshal(s)
			if _, err := managedsettings.Decode(data); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
	if _, err := managedsettings.Encode(context.Background()); err == nil {
		t.Fatal("missing settings accepted")
	}
}
func TestLongRunningTurnDoesNotReplacePinnedDefaults(t *testing.T) {
	s := settingstest.Snapshot("company-a", "1", "Old turn instruction.")
	s.Policy.IssuedAt = time.Now().Add(-time.Hour)
	s.Policy.ExpiresAt = s.Policy.IssuedAt.Add(10 * time.Minute)
	// This does not authorize inference. That is the host's current policy gate.
	data, _ := json.Marshal(s)
	if _, err := managedsettings.Decode(data); err != nil {
		t.Fatal(err)
	}
}
