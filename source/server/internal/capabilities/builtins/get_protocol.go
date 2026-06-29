package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/protocols"
)

type getProtocolCap struct{}

// GetProtocol constructs the get_protocol capability: returns a workflow
// protocol's full body by name.
func GetProtocol() capabilities.Capability { return getProtocolCap{} }

func (getProtocolCap) Name() string            { return "get_protocol" }
func (getProtocolCap) Tier() capabilities.Tier { return capabilities.TierR }
func (getProtocolCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (getProtocolCap) Description() string {
	return "Return the full text of a Cercano workflow protocol by name (e.g. design-decisions, systematic-debugging, verification-strategy, compute-before-simulate). Pull a protocol when its trigger applies, then follow it."
}
func (getProtocolCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"Protocol name, e.g. design-decisions."}}}`)
}

type getProtocolArgs struct {
	Name string `json:"name"`
}

func (getProtocolCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a getProtocolArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("get_protocol: parse args: %w", err)
	}
	if a.Name == "" {
		return nil, errors.New("get_protocol: name is required")
	}
	p, ok := protocols.Get(a.Name)
	if !ok {
		var names []string
		for _, b := range protocols.Builtins() {
			names = append(names, b.Name)
		}
		return nil, fmt.Errorf("get_protocol: unknown protocol %q; available: %s", a.Name, strings.Join(names, ", "))
	}
	return capabilities.NewTextResult(p.Body), nil
}
