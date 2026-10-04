package v1_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bryancostanich/Cercano/source/enterpriseapi/conformance"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestPublishedPolicySchema(t *testing.T) {
	data, err := os.ReadFile("policy.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conformance.Cases() {
		t.Run(c.Name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal(c.Payload, &value); err != nil {
				t.Fatal(err)
			}
			err := resolved.Validate(value)
			if err == nil {
				var p v1.Policy
				p, err = v1.DecodePolicy(c.Payload)
				if err == nil {
					err = p.Validate(c.Scope, c.Now)
				}
			}
			if (err == nil) != c.Valid {
				t.Fatalf("schema/semantics disagree with expected validity %v: %v", c.Valid, err)
			}
		})
	}
	var value map[string]any
	if err = json.Unmarshal(conformance.Cases()[0].Payload, &value); err != nil {
		t.Fatal(err)
	}
	delete(value, "allowed_routes")
	if err = resolved.Validate(value); err == nil {
		t.Fatal("schema permits missing allowed routes")
	}
}
