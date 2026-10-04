package v1_test

import (
	"bytes"
	"testing"

	"github.com/bryancostanich/Cercano/source/enterpriseapi/conformance"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func TestSharedContract(t *testing.T) {
	for _, c := range conformance.Cases() {
		t.Run(c.Name, func(t *testing.T) {
			p, err := v1.DecodePolicy(c.Payload)
			if err == nil {
				err = p.Validate(c.Scope, c.Now)
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v: %v", c.Valid, err)
			}
		})
	}
}

func TestStrictDecoding(t *testing.T) {
	base := conformance.Cases()[0].Payload
	for name, data := range map[string][]byte{
		"duplicate":        bytes.Replace(base, []byte(`"revision": 7`), []byte(`"revision": 7, "revision": 8`), 1),
		"nested-duplicate": bytes.Replace(base, []byte(`"organization_id": "org-a"`), []byte(`"organization_id": "org-a", "organization_id": "org-b"`), 1),
		"unknown":          bytes.Replace(base, []byte(`"revision": 7`), []byte(`"revision": 7, "allow_all": true`), 1),
		"case-alias":       bytes.Replace(base, []byte(`"revision": 7`), []byte(`"revision": 7, "Revision": 8`), 1),
		"missing-override": bytes.Replace(base, []byte(`, "allow_developer_override": false`), nil, 1),
		"invalid-UTF8":     append(append([]byte(nil), base...), 0xff),
		"trailing":         append(append([]byte(nil), base...), []byte(`{}`)...),
		"oversized":        bytes.Repeat([]byte(" "), v1.MaxPolicyBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := v1.DecodePolicy(data); err == nil {
				t.Fatal("accepted invalid JSON")
			}
		})
	}
}
