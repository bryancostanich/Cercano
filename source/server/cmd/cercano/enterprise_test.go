package main

import (
	"bytes"
	"encoding/json"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"strings"
	"testing"
	"time"

	"cercano/source/server/pkg/proto"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestEnterpriseStatusOutput(t *testing.T) {
	s := &proto.EnterpriseStatus{Managed: true, EnforcementActive: true, Connected: true, Usable: true, MembershipKnown: true, OrganizationName: "Example", OrganizationId: "org", TeamId: "team", TeamName: "Engineering\x1b[2J\nForged status", Revision: 7, ValidUntil: "2026-10-04T05:00:00Z"}
	text := enterpriseStatusText(s)
	for _, want := range []string{"Enterprise managed", `Organization: "Example"`, `Team: "Engineering\x1b[2J\nForged status"`, "Applied policy revision: 7", "Status: Ready"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "\x1b") || strings.Contains(text, "\nForged status") {
		t.Fatal("terminal controls escaped into output")
	}
	var out bytes.Buffer
	if err := writeEnterpriseStatus(&out, s, true); err != nil {
		t.Fatal(err)
	}
	var decoded proto.EnterpriseStatus
	if err := protojson.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TeamName != s.TeamName || !decoded.MembershipKnown {
		t.Fatal("JSON did not preserve status")
	}
	s.TeamId, s.TeamName = "", ""
	if !strings.Contains(enterpriseStatusText(s), "None assigned") {
		t.Fatal("no-team state unclear")
	}
	s.MembershipKnown = false
	if !strings.Contains(enterpriseStatusText(s), "Details unavailable") {
		t.Fatal("missing metadata mistaken for no team")
	}
	s.Error = "unavailable"
	if !strings.Contains(enterpriseStatusText(s), "last verified policy") {
		t.Fatal("outage state unclear")
	}
	s.Usable = false
	if !strings.Contains(enterpriseStatusText(s), "Managed work is blocked") {
		t.Fatal("expired state unclear")
	}
	s.Connected = false
	if !strings.Contains(enterpriseStatusText(s), "Not signed in") {
		t.Fatal("logged out state unclear")
	}
	s.Managed = false
	if text = enterpriseStatusText(s); text != "Mode: Personal settings\n" {
		t.Fatal(text)
	}
}

func TestEnterprisePolicyAndSkillsOutput(t *testing.T) {
	p := v1.Policy{SchemaVersion: v1.Version, Scope: v1.Scope{OrganizationID: "00000000-0000-0000-0000-000000000001", UserID: "00000000-0000-0000-0000-000000000002", HostID: "00000000-0000-0000-0000-000000000003"}, Revision: 7, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), MinimumClientVersion: "1.0.0", AllowedRoutes: []v1.Route{{ID: "primary", Provider: "openai", Endpoint: "https://models.example/v1", Model: "approved", Placement: "external"}}, TaskDefaults: []v1.TaskDefault{{Task: "chat", Destination: "primary", Quality: "standard", RouteID: "primary", FallbackRouteIDs: []string{}, AllowDeveloperOverride: true}}, Skills: []v1.SkillAssignment{}}
	raw, _ := json.Marshal(p)
	var out bytes.Buffer
	if err := writeEnterprisePolicy(&out, raw, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"revision 7", `"approved"`, "https://models.example/v1", "Developer may choose another approved route"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
	out.Reset()
	if err := writeEnterprisePolicy(&out, raw, true); err != nil {
		t.Fatal(err)
	}
	var parsed v1.Policy
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil || parsed.Revision != 7 {
		t.Fatal("invalid policy JSON", err)
	}
	p.AllowedRoutes = []v1.Route{}
	p.TaskDefaults = []v1.TaskDefault{}
	raw, _ = json.Marshal(p)
	out.Reset()
	if err := writeEnterprisePolicy(&out, raw, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "All managed model requests are blocked") {
		t.Fatal("empty ceiling unclear")
	}
	out.Reset()
	catalog := &proto.ListSkillsResponse{Skills: []*proto.SkillInfo{{Name: "builtin-only", Source: "builtin"}, {Name: "enterprise/org/review", DisplayName: "Review", Source: "enterprise", Version: "2", Description: "Review carefully"}}}
	if err := writeEnterpriseSkills(&out, catalog, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "builtin-only") || !strings.Contains(out.String(), `"Review" — version "2"`) {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := writeEnterpriseSkills(&out, catalog, true); err != nil {
		t.Fatal(err)
	}
	var selected proto.ListSkillsResponse
	if err := protojson.Unmarshal(out.Bytes(), &selected); err != nil || len(selected.Skills) != 1 {
		t.Fatal("invalid skill catalog", err)
	}
	out.Reset()
	skill := &proto.GetSkillResponse{Name: "enterprise/org/review", Version: "2", Source: "enterprise", Content: "# Review\nCheck errors.\n\x1b[2J"}
	if err := writeEnterpriseSkill(&out, skill, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "# Review\nCheck errors.") {
		t.Fatal(out.String())
	}
	skill.Source = "builtin"
	if err := writeEnterpriseSkill(&out, skill, false); err == nil {
		t.Fatal("non-enterprise skill accepted")
	}
}
