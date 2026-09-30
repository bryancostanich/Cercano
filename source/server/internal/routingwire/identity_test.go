package routingwire

import (
	"cercano/source/server/pkg/accountidentity"
	"cercano/source/server/pkg/config"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestAccountIdentitySurvivesWireRoundTrip(t *testing.T) {
	p := config.CloudProfile{Name: "account-key", AccountIdentity: accountidentity.Identity{Email: "person@example.com", Name: "Person"}}
	serialized, err := proto.Marshal(Profile(p, config.ModelProfiles{}))
	if err != nil {
		t.Fatal(err)
	}
	var message wire.CloudProfileInfo
	if err := proto.Unmarshal(serialized, &message); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProfile(&message)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Name != p.Name || decoded.AccountIdentity != p.AccountIdentity {
		t.Fatal("identity lost or key changed")
	}
	if _, err := DecodeProfile(&wire.CloudProfileInfo{Name: "legacy"}); err != nil {
		t.Fatal("older server metadata rejected")
	}
}
