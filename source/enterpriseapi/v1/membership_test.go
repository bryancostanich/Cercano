package v1

import (
	"encoding/json"
	"testing"
)

func TestEffectivePolicyMembershipCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":"1","policy":{}}`,
		`{"schema_version":"1","policy":{},"membership":{"organization_id":"org","organization_name":"Example","user_id":"member","role":"developer"}}`,
		`{"schema_version":"1","policy":{},"membership":{"organization_id":"org","organization_name":"Example","user_id":"member","role":"developer","team_id":"team","team_name":"Engineering"}}`,
	} {
		var response EffectivePolicyResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip EffectivePolicyResponse
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if (response.Membership == nil) != (roundTrip.Membership == nil) {
			t.Fatal("absent membership lost")
		}
		if response.Membership != nil && *response.Membership != *roundTrip.Membership {
			t.Fatal("membership changed")
		}
		// Existing clients can still consume the signed policy without the display metadata.
		var oldResponse struct {
			SchemaVersion string       `json:"schema_version"`
			Policy        SignedPolicy `json:"policy"`
		}
		if err := json.Unmarshal(encoded, &oldResponse); err != nil {
			t.Fatal(err)
		}
		if oldResponse.Policy != response.Policy {
			t.Fatal("signed policy changed")
		}
	}
}

func TestSyncFailureCodesAreBounded(t *testing.T) {
	for _, code := range []string{"unavailable", "verification_failed", "authorization_denied", "clock_changed", "credential_store_unavailable"} {
		if !ValidSyncErrorCode(code) {
			t.Fatal(code)
		}
	}
	for _, code := range []string{"", "provider-key-secret", "arbitrary error body", "Unavailable"} {
		if ValidSyncErrorCode(code) {
			t.Fatal("unbounded diagnostic accepted")
		}
	}
}
