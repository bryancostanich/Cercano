package modelmetadata

// Vision represents the vision capability status of a model.
//
// VisionUnknown is deliberately the ZERO value. A zero Evidence — from a map
// miss, an unset struct field, or a decoded message with the field absent —
// must mean "no evidence", and callers compare against VisionUnknown to detect
// exactly that. Making unknown anything other than the zero value creates a
// fourth, nameless state that compares equal to none of the three and silently
// fails those checks.
type Vision int

const (
	VisionUnknown Vision = iota
	VisionSupported
	VisionUnsupported
)

// String renders the capability for logs and test failures.
func (v Vision) String() string {
	switch v {
	case VisionSupported:
		return "supported"
	case VisionUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// Identity represents a model's unique identifier without credentials
type Identity struct {
	Provider string
	BaseURL  string
	Route    string
	Model    string
}

// Evidence contains metadata about a model's capabilities
type Evidence struct {
	ContextWindow int
	Vision        Vision
}

// Normalized ensures malformed or absent evidence cannot grant a capability.
func (e Evidence) Normalized() Evidence {
	if e.ContextWindow < 0 {
		e.ContextWindow = 0
	}
	if e.Vision != VisionSupported && e.Vision != VisionUnsupported {
		e.Vision = VisionUnknown
	}
	return e
}

// Entry combines Identity with Evidence
type Entry struct {
	Identity Identity
	Evidence Evidence
}

// Snapshot is a collection of model metadata entries
type Snapshot []Entry

// Lookup finds evidence for an exact identity match
func (s Snapshot) Lookup(identity Identity) (Evidence, bool) {
	for _, entry := range s {
		if entry.Identity == identity {
			return entry.Evidence.Normalized(), true
		}
	}
	return Evidence{}, false
}
