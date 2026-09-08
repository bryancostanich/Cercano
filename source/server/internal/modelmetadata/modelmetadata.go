package modelmetadata

// Vision represents the vision capability status of a model
type Vision string

const (
	VisionUnknown     Vision = "unknown"
	VisionSupported   Vision = "supported"
	VisionUnsupported Vision = "unsupported"
)

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
