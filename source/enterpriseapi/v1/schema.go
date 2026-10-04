package v1

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed policy.schema.json
var policySchema []byte

// Compile once. The schema contains only local references and performs no I/O.
var resolvedSchema = sync.OnceValues(func() (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(policySchema, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
})

func validateShape(data []byte) error {
	schema, err := resolvedSchema()
	if err != nil {
		return err
	}
	var value any
	if err = json.Unmarshal(data, &value); err != nil {
		return err
	}
	return schema.Validate(value)
}
