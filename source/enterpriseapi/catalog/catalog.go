// Package catalog publishes a credential-free snapshot of Cercano's built-in
// provider, model and task choices. It is editor metadata, never authorization.
package catalog

import _ "embed"

//go:embed catalog.json
var document []byte

// JSON returns an independent copy of the versioned catalog document.
func JSON() []byte { return append([]byte(nil), document...) }
