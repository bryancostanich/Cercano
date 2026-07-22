package persistence

import (
	"strings"
	"testing"
)

func TestBuildSuggestNextPromptPromptUsesStructuredActionContract(t *testing.T) {
	prompt := buildSuggestNextPromptPrompt("Recap: user approved Option B.", "[assistant]\nShould I proceed with Option B?\n")

	for _, want := range []string{
		"ghost-text tab-complete suggestions",
		"Infer the current task state",
		"most useful next user action",
		"output nothing if no high-confidence useful action exists",
		"Output exactly ONE next prompt",
		"Output an empty string if the next action is unclear or low-value",
		"Good suggestion classes:",
		"Approve a waiting decision",
		"Choose among options",
		"Ask for verification when code changed",
		"Continue approved implementation",
		"Ask for a concise summary after completed work",
		"Ask to checkpoint/land only when work is complete and verified",
		"Avoid bad suggestions:",
		"Do not suggest work already completed",
		"Do not suggest generic prompts like 'what next?', 'continue', or 'tell me more'",
		"Do not suggest running tests unless verification is actually pending",
		"Do not suggest checkpoint/land if edits are still in progress or unverified",
		"Recap: user approved Option B.",
		"[assistant]\nShould I proceed with Option B?",
		"Next useful user prompt:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, bad := range []string{
		"predict what a user might reasonably ask next",
		"Next prompt:",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("prompt still contains old generic autoprompt contract %q:\n%s", bad, prompt)
		}
	}
}

func TestBuildSuggestNextPromptPromptOmitsBlankRecap(t *testing.T) {
	prompt := buildSuggestNextPromptPrompt("   ", "[assistant]\nDone.\n")
	if strings.Contains(prompt, "Recap of the conversation so far:") {
		t.Fatalf("blank recap should be omitted:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Most recent turns:\n[assistant]\nDone.\n") {
		t.Fatalf("tail missing from prompt:\n%s", prompt)
	}
}

func TestSanitizeSuggestionAllowsEmptyNoSuggestion(t *testing.T) {
	if got := SanitizeSuggestion("   \n  "); got != "" {
		t.Fatalf("SanitizeSuggestion blank output = %q, want empty", got)
	}
}
