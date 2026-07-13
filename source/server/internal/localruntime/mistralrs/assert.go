package mistralrs

import "cercano/source/server/internal/localruntime"

// Compile-time proof that *Provider satisfies the runtime Provider interface,
// so a signature drift is caught here rather than at the main.go wiring site.
var _ localruntime.Provider = (*Provider)(nil)
