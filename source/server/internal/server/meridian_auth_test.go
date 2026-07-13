package server

import (
	"testing"

	"cercano/source/server/pkg/config"
)

// TestMeridianAuthMissing locks the activation gate: a meridian-routed profile
// with no Claude OAuth token must report auth missing (so SetActiveCloudProfile
// refuses it with guidance), while non-meridian routes never gate on it and an
// authed meridian profile passes.
func TestMeridianAuthMissing(t *testing.T) {
	orig := meridianHasAuth
	t.Cleanup(func() { meridianHasAuth = orig })

	meridianHasAuth = func() bool { return false }
	if !meridianAuthMissing(config.CloudProfile{Route: "meridian"}) {
		t.Error("meridian profile with no Claude token: want auth missing")
	}
	if meridianAuthMissing(config.CloudProfile{Route: "direct"}) {
		t.Error("non-meridian route must never gate on Claude auth")
	}

	meridianHasAuth = func() bool { return true }
	if meridianAuthMissing(config.CloudProfile{Route: "meridian"}) {
		t.Error("meridian profile with a Claude token: want pass")
	}
}
