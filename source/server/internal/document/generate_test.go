package document

import (
	"strings"
	"testing"
)

func TestBuildPrompt_Minimal(t *testing.T) {
	sym := Symbol{
		Name: "Complete",
		Kind: KindMethod,
		Body: "func (e *Engine) Complete(ctx context.Context, prompt string) (string, error) {\n\treturn \"\", nil\n}",
	}
	prompt := BuildPrompt(sym, StyleMinimal)

	if !strings.Contains(prompt, "Complete") {
		t.Error("prompt should contain symbol name")
	}
	if !strings.Contains(prompt, "concise") {
		t.Error("minimal style should mention concise")
	}
	if !strings.Contains(prompt, sym.Body) {
		t.Error("prompt should contain symbol body")
	}
}

func TestBuildPrompt_Detailed(t *testing.T) {
	sym := Symbol{
		Name: "Complete",
		Kind: KindFunction,
		Body: "func Complete(x int) string { return \"\" }",
	}
	prompt := BuildPrompt(sym, StyleDetailed)

	if !strings.Contains(prompt, "thorough") {
		t.Error("detailed style should mention thorough")
	}
	if !strings.Contains(prompt, "parameter descriptions") {
		t.Error("detailed style should mention parameter descriptions")
	}
}

func TestFormatAsGoDoc(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single line",
			input: "Complete sends a prompt to the engine.",
			want:  "// Complete sends a prompt to the engine.",
		},
		{
			name:  "multi line",
			input: "Complete sends a prompt to the engine.\nIt returns the generated text.",
			want:  "// Complete sends a prompt to the engine.\n// It returns the generated text.",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n  ",
			want:  "",
		},
		{
			name:  "blank line in middle",
			input: "First paragraph.\n\nSecond paragraph.",
			want:  "// First paragraph.\n//\n// Second paragraph.",
		},
		{
			name:  "trailing spaces stripped",
			input: "Line with trailing spaces.   ",
			want:  "// Line with trailing spaces.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAsGoDoc(tt.input)
			if got != tt.want {
				t.Errorf("FormatAsGoDoc(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatAsGoDoc_StripsPrefixes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips // prefix",
			input: "// Complete does something.",
			want:  "// Complete does something.",
		},
		{
			name:  "strips multi // prefix",
			input: "// Line one.\n// Line two.",
			want:  "// Line one.\n// Line two.",
		},
		{
			name:  "strips /* */ markers",
			input: "/* Complete does something. */",
			want:  "// Complete does something.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAsGoDoc(tt.input)
			if got != tt.want {
				t.Errorf("FormatAsGoDoc(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
