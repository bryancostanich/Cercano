package config

// CloudModelChoice is a built-in fallback suggestion, not a live account inventory.
type CloudModelChoice struct {
	ID          string
	DisplayName string
}

// ClaudeModelChoices is shared by the CLI and enterprise catalog export.
func ClaudeModelChoices() []CloudModelChoice {
	return []CloudModelChoice{
		{ID: "claude-fable-5", DisplayName: "Claude Fable 5 (most capable)"},
		{ID: "claude-opus-5", DisplayName: "Claude Opus 5 (latest)"},
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6 (balanced)"},
		{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5"},
		{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5 (fastest)"},
	}
}
