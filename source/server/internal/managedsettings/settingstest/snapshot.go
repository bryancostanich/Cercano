// Package settingstest supplies disposable policy bundles for integration tests.
// These unsigned fixtures must never be used as an enterprise trust source.
package settingstest

import (
	"cercano/source/server/internal/managedsettings"
	"crypto/sha256"
	"fmt"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"time"
)

func Snapshot(org, version, content string) managedsettings.Snapshot {
	now := time.Now().UTC()
	id := org + "/review"
	return managedsettings.Snapshot{
		Policy: v1.Policy{SchemaVersion: v1.Version, Scope: v1.Scope{OrganizationID: org, UserID: "member", HostID: "host"}, Revision: 1, IssuedAt: now, ExpiresAt: now.Add(v1.MaxLease), MinimumClientVersion: "1.0.0", AllowedRoutes: []v1.Route{{ID: "approved", Provider: "openai", Endpoint: "https://models.example/v1", Model: "approved", Placement: "external"}}, TaskDefaults: []v1.TaskDefault{{Task: "chat", Destination: "primary", Quality: "standard", RouteID: "approved", FallbackRouteIDs: []string{}}}, Skills: []v1.SkillAssignment{{ID: id, Version: version, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), SizeBytes: len(content)}}},
		Skills: []v1.SkillContentResponse{{SchemaVersion: v1.Version, ID: id, Version: version, Name: "Review", Description: "Review proposed changes.", Content: content}},
	}
}
