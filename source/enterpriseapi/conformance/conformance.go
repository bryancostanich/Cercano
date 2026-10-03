// Package conformance supplies the same protocol examples to server and client
// tests. Times and identities are fixed so the examples remain reproducible.
package conformance

import (
	_ "embed"
	"encoding/json"
	"time"

	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

//go:embed fixtures/policy.json
var policy []byte

type Case struct {
	Name    string
	Payload []byte
	Scope   v1.Scope
	Now     time.Time
	Valid   bool
}

// Cases returns independent payloads. It covers semantic rejection as well as
// shape decoding; cryptographic verification is not implemented by this slice.
func Cases() []Case {
	now := time.Date(2026, 10, 3, 12, 5, 0, 0, time.UTC)
	scope := v1.Scope{OrganizationID: "org-a", UserID: "member-a", HostID: "host-a"}
	mutate := func(fn func(*v1.Policy)) []byte {
		var p v1.Policy
		if err := json.Unmarshal(policy, &p); err != nil {
			panic(err)
		}
		fn(&p)
		b, err := json.Marshal(p)
		if err != nil {
			panic(err)
		}
		return b
	}
	return []Case{
		{"allowed", append([]byte(nil), policy...), scope, now, true},
		{"deny-all", mutate(func(p *v1.Policy) { p.AllowedRoutes = []v1.Route{}; p.TaskDefaults = []v1.TaskDefault{} }), scope, now, true},
		{"forbidden-fallback", mutate(func(p *v1.Policy) { p.TaskDefaults[0].FallbackRouteIDs = []string{"unapproved"} }), scope, now, false},
		{"expired", append([]byte(nil), policy...), scope, now.Add(10 * time.Minute), false},
		{"wrong-tenant", append([]byte(nil), policy...), v1.Scope{OrganizationID: "org-b", UserID: scope.UserID, HostID: scope.HostID}, now, false},
		{"wrong-user", append([]byte(nil), policy...), v1.Scope{OrganizationID: scope.OrganizationID, UserID: "member-b", HostID: scope.HostID}, now, false},
		{"wrong-host", append([]byte(nil), policy...), v1.Scope{OrganizationID: scope.OrganizationID, UserID: scope.UserID, HostID: "host-b"}, now, false},
		{"unsupported-version", mutate(func(p *v1.Policy) { p.SchemaVersion = "2" }), scope, now, false},
		{"conflicting-skills", mutate(func(p *v1.Policy) { s := p.Skills[0]; s.Version = "2"; p.Skills = append(p.Skills, s) }), scope, now, false},
		{"oversized-lease", mutate(func(p *v1.Policy) { p.ExpiresAt = p.IssuedAt.Add(time.Hour) }), scope, now, false},
		{"implicit-permissions", mutate(func(p *v1.Policy) { p.AllowedRoutes = nil }), scope, now, false},
		{"credential-in-endpoint", mutate(func(p *v1.Policy) { p.AllowedRoutes[0].Endpoint = "https://user:secret@models.example.com/v1" }), scope, now, false},
	}
}
