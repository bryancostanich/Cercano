package modelevidence

import (
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/modelmetadata"
)

// shippedEvidence is the curated knowledge Cercano ships about model families
// it has verified, keyed on identity rather than on the model name alone.
//
// This exists so that tightening image routing does not silently withdraw
// vision from providers where it demonstrably works today. Before this
// package, cloudfactory marked every OpenAI-compatible transport
// SupportsVision=true; that flag was wrong as *model* evidence but it was not
// wrong about Anthropic and OpenAI's current multimodal families. Those stay
// supported here, on the record, instead of regressing to "unknown".
//
// Entries are affirmative or explicitly negative. A family absent from this
// table is unknown, not unsupported.
func shippedEvidence(id modelmetadata.Identity) modelmetadata.Evidence {
	ev := modelmetadata.Evidence{Vision: shippedVision(id)}
	// contextmeter owns the published per-family windows. Reuse it rather than
	// duplicating a second table that could drift from the meter's denominator.
	if window, ok := contextmeter.KnownModelMax(id.Model); ok {
		ev.ContextWindow = window
	}
	return ev
}

// shippedVision reports image-input capability for verified families.
//
// Matching is on model-name family within a recognized vendor host. The host
// check matters: an arbitrary gateway can serve a model called "gpt-4o" that
// is not OpenAI's, and inheriting OpenAI's capability for it would be exactly
// the unfounded inference this effort removes.
func shippedVision(id modelmetadata.Identity) modelmetadata.Vision {
	m := strings.ToLower(strings.TrimSpace(id.Model))
	if m == "" {
		return modelmetadata.VisionUnknown
	}
	switch vendorOf(id) {
	case vendorAnthropic:
		// Claude 3 and later are multimodal across Opus/Sonnet/Haiku.
		if strings.Contains(m, "claude") {
			return modelmetadata.VisionSupported
		}
	case vendorOpenAI:
		switch {
		// Text-only OpenAI models we can state negatively with confidence.
		case strings.Contains(m, "gpt-3.5"), strings.Contains(m, "text-embedding"):
			return modelmetadata.VisionUnsupported
		case strings.Contains(m, "gpt-4o"), strings.Contains(m, "gpt-4.1"),
			strings.Contains(m, "gpt-5"), strings.Contains(m, "o3"), strings.Contains(m, "o4"):
			return modelmetadata.VisionSupported
		}
	case vendorGemini:
		if strings.Contains(m, "gemini") {
			return modelmetadata.VisionSupported
		}
	}
	return modelmetadata.VisionUnknown
}

type vendor int

const (
	vendorUnknown vendor = iota
	vendorAnthropic
	vendorOpenAI
	vendorGemini
)

// vendorOf identifies the vendor from the identity's endpoint, falling back to
// the provider name for routes that carry no base URL (Anthropic's
// subscription route, for example, uses the SDK default endpoint).
func vendorOf(id modelmetadata.Identity) vendor {
	host := hostOf(id.BaseURL)
	switch {
	case strings.Contains(host, "anthropic.com"):
		return vendorAnthropic
	case strings.Contains(host, "openai.com"):
		return vendorOpenAI
	case strings.Contains(host, "googleapis.com"):
		return vendorGemini
	}
	if host != "" {
		// A third-party gateway. Its model names may collide with vendor names
		// but carry none of the vendor's guarantees.
		return vendorUnknown
	}
	switch strings.ToLower(strings.TrimSpace(id.Provider)) {
	case "anthropic":
		return vendorAnthropic
	case "openai":
		return vendorOpenAI
	case "gemini", "google":
		return vendorGemini
	}
	return vendorUnknown
}
