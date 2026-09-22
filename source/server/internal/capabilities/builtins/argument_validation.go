package builtins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"cercano/source/server/internal/capabilities"
	"github.com/google/jsonschema-go/jsonschema"
)

var argumentSchemas sync.Map // schema text -> immutable resolved validator

// decodeDeclaredArguments is for closed-object filesystem/command contracts.
// Validate before side effects, never silently translate misspelled fields.
func decodeDeclaredArguments(raw json.RawMessage, cap capabilities.Capability, out any) error {
	var instance map[string]any
	if err := json.Unmarshal(raw, &instance); err != nil || instance == nil {
		return fmt.Errorf("%s: arguments must be one JSON object; no action performed", cap.Name())
	}
	schemaText := string(cap.Schema())
	var shape struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal([]byte(schemaText), &shape); err != nil {
		return fmt.Errorf("%s: invalid argument schema", cap.Name())
	}
	accepted := make([]string, 0, len(shape.Properties))
	for key := range shape.Properties {
		accepted = append(accepted, key)
	}
	sort.Strings(accepted)
	for key := range instance {
		if _, ok := shape.Properties[key]; !ok {
			return fmt.Errorf("%s: unsupported argument %q; accepted fields: %s; no action performed", cap.Name(), key, strings.Join(accepted, ", "))
		}
	}
	for _, key := range shape.Required {
		if _, ok := instance[key]; !ok {
			return fmt.Errorf("%s: %s is required; no action performed", cap.Name(), key)
		}
	}
	cached, ok := argumentSchemas.Load(schemaText)
	if !ok {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
			return fmt.Errorf("%s: invalid argument schema", cap.Name())
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return fmt.Errorf("%s: invalid argument schema", cap.Name())
		}
		cached, _ = argumentSchemas.LoadOrStore(schemaText, resolved)
	}
	if err := cached.(*jsonschema.Resolved).Validate(instance); err != nil {
		// Identify the invalid property using schema metadata, never its value.
		for _, key := range accepted {
			value, present := instance[key]
			if !present {
				continue
			}
			var prop jsonschema.Schema
			if json.Unmarshal(shape.Properties[key], &prop) != nil {
				continue
			}
			resolved, e := prop.Resolve(nil)
			if e == nil && resolved.Validate(value) != nil {
				return fmt.Errorf("%s: invalid %s: must satisfy the declared type and bounds; no action performed", cap.Name(), key)
			}
		}
		// Validation errors can echo supplied values. Return only schema metadata.
		return fmt.Errorf("%s: arguments violate the declared types or bounds (fields: %s); no action performed", cap.Name(), strings.Join(accepted, ", "))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%s: parse args: %w; no action performed", cap.Name(), err)
	}
	return nil
}
