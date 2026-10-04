package main

import (
	"bytes"
	"strings"
	"testing"

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
