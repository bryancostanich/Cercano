package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"charm.land/lipgloss/v2"
)

// stripAnsiCSI is defined in confirm_test.go (same package). Reused here.

func TestToolEntry_FoldedRender(t *testing.T) {
	e := ToolEntry{
		ToolName:      "read_file",
		ArgsSummary:   `path="main.go"`,
		Status:        ToolStatusComplete,
		ResultSummary: "32 lines",
		Folded:        true,
	}
	s := stripAnsiCSI(renderToolEntry(e, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.Contains(s, "▸ read_file") {
		t.Errorf("expected fold marker + name, got: %q", s)
	}
	if !strings.Contains(s, "32 lines") {
		t.Errorf("expected result summary, got: %q", s)
	}
	if strings.Count(s, "\n") > 0 {
		t.Errorf("folded should be one line, got newlines in: %q", s)
	}
}

func TestToolEntry_DaylightHeaderAndArgsUseReadableMutedColor(t *testing.T) {
	daylight := builtinToolPaletteForTest("daylight")
	out := renderToolEntry(ToolEntry{
		ToolName:    "Bash",
		ArgsSummary: "bash -lc 'bash -n source/server/scripts/cercano-launcher.sh'",
		Status:      ToolStatusInProgress,
		Folded:      true,
	}, 100, false, theme.NewStyles(daylight), render.NewMarkdown(theme.MarkdownStyle(daylight)))
	if strings.Contains(out, "\x1b[2m") {
		t.Fatalf("tool header should not rely on terminal faint mode in daylight, got %q", out)
	}
	if !strings.Contains(out, "38;2;59;48;32") {
		t.Fatalf("tool marker/name should use daylight primary (#3B3020), got %q", out)
	}
	if !strings.Contains(out, "38;2;111;106;85") {
		t.Fatalf("tool args/status should use readable daylight muted (#6F6A55), got %q", out)
	}
}

func TestToolEntry_DaylightExpandedRawArgsUseReadableMutedColor(t *testing.T) {
	daylight := builtinToolPaletteForTest("daylight")
	out := renderToolEntry(ToolEntry{
		ToolName:    "dispatch",
		ArgsSummary: "conversation_id= cwd=/Users/bryancostanich/git_repos/bryan_costanich/Cercano",
		FullArgs:    `{"intent":"offload read-heavy recon","path":"/Users/bryancostanich/git_repos/bryan_costanich/Cercano","task":"Investigate source/clients/cli tool output rendering"}`,
		Status:      ToolStatusComplete,
		Folded:      false,
	}, 80, false, theme.NewStyles(daylight), render.NewMarkdown(theme.MarkdownStyle(daylight)))
	plain := stripAnsiCSI(out)
	if !strings.Contains(plain, "args:") {
		t.Fatalf("test setup expected expanded raw args line, got:\n%s", plain)
	}
	if !strings.Contains(out, "38;2;111;106;85margs:") {
		t.Fatalf("expanded raw args should use readable daylight muted (#6F6A55), got %q", out)
	}
}

func builtinToolPaletteForTest(name string) theme.Palette {
	for _, builtin := range theme.BuiltinThemes() {
		if builtin.Name == name {
			return builtin.Palette
		}
	}
	return theme.Cracker()
}

func TestToolEntry_SessionControlUsesProductLabelAndHidesArgs(t *testing.T) {
	e := ToolEntry{
		ToolName:      "suggest_autonomous",
		ArgsSummary:   "constraints=[very long raw args] goal=ship",
		Status:        ToolStatusComplete,
		ResultSummary: "5ms",
		Folded:        true,
	}
	s := stripAnsiCSI(renderToolEntry(e, 100, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.Contains(s, "Autonomous run brief") {
		t.Fatalf("session-control tool should use product label, got: %q", s)
	}
	for _, bad := range []string{"suggest_autonomous", "constraints=[", "goal=ship", "DESTRUCTIVE", "⚠"} {
		if strings.Contains(s, bad) {
			t.Fatalf("session-control tool row leaked %q: %q", bad, s)
		}
	}
}

func TestToolEntry_SessionControlExpandedStillHidesArgs(t *testing.T) {
	e := ToolEntry{
		ToolName:      "suggest_autonomous",
		ArgsSummary:   "constraints=[very long raw args]",
		FullArgs:      `{"goal":"ship","constraints":["do not push"]}`,
		Status:        ToolStatusComplete,
		ResultSummary: "5ms",
		Folded:        false,
	}
	s := stripAnsiCSI(renderToolEntry(e, 100, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.Contains(s, "Autonomous run brief") {
		t.Fatalf("session-control tool should use product label, got: %q", s)
	}
	for _, bad := range []string{"suggest_autonomous", "constraints=[", `"goal"`, `"constraints"`, "args:"} {
		if strings.Contains(s, bad) {
			t.Fatalf("expanded session-control tool leaked %q: %q", bad, s)
		}
	}
}

func TestHumanizeArgs_SessionControlSuppressesRawArgs(t *testing.T) {
	got := humanizeArgs("suggest_autonomous", `{"goal":"ship","constraints":["do not push"]}`, "/repo", "/Users/me")
	if got != "" {
		t.Fatalf("session-control args should be suppressed, got %q", got)
	}
}

func TestToolEntry_ArgsColumnAligned(t *testing.T) {
	// Short tool names pad to a fixed column so args start at the same offset
	// regardless of name length.
	short := stripAnsiCSI(renderToolEntry(ToolEntry{ToolName: "LS", ArgsSummary: "X", Status: ToolStatusComplete, Folded: true}, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	long := stripAnsiCSI(renderToolEntry(ToolEntry{ToolName: "Bash", ArgsSummary: "X", Status: ToolStatusComplete, Folded: true}, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if strings.Index(short, "X") != strings.Index(long, "X") {
		t.Errorf("args column not aligned: LS arg at %d, Bash arg at %d\n%q\n%q",
			strings.Index(short, "X"), strings.Index(long, "X"), short, long)
	}
}

func TestToolEntry_StatusRightAligned(t *testing.T) {
	e := ToolEntry{ToolName: "Bash", ArgsSummary: "x", Status: ToolStatusComplete, ResultSummary: "exit 1 · 686ms", Folded: true}
	const w = 60
	s := stripAnsiCSI(renderToolEntry(e, w, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.HasSuffix(s, "✓ exit 1 · 686ms") {
		t.Errorf("status should be at the right edge, got: %q", s)
	}
	if lipgloss.Width(s) != w {
		t.Errorf("right-aligned line should fill width %d, got width %d: %q", w, lipgloss.Width(s), s)
	}
}

func TestToolEntry_ExpandedRender(t *testing.T) {
	e := ToolEntry{
		ToolName:    "read_file",
		ArgsSummary: `path="main.go"`,
		FullArgs:    `{"path":"main.go"}`,
		FullResult:  "package main\n\nimport ...",
		Status:      ToolStatusComplete,
		Folded:      false,
	}
	s := stripAnsiCSI(renderToolEntry(e, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.Contains(s, "▾ read_file") {
		t.Errorf("expected unfold marker, got: %q", s)
	}
	if !strings.Contains(s, `"path":"main.go"`) {
		t.Errorf("expanded should show full args, got: %q", s)
	}
}

func TestToolEntry_ExpandedInProgressDoesNotRenderRawArgsAsOutput(t *testing.T) {
	longArgs := `{"cmd":["bash","-lc","printf 'first\\n'; printf 'second\\n'; printf 'third\\n'"]}`
	e := ToolEntry{
		ToolName:    "Bash",
		ArgsSummary: "bash -lc 'printf ...'",
		FullArgs:    longArgs,
		Status:      ToolStatusInProgress,
		Folded:      false,
	}
	s := stripAnsiCSI(renderToolEntry(e, 50, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if strings.Contains(s, "args:") || strings.Contains(s, `"cmd"`) {
		t.Fatalf("expanded running tool should not render raw args as output:\n%s", s)
	}
	if !strings.Contains(s, "no output yet") {
		t.Fatalf("expanded running tool should show a concise no-output state, got:\n%s", s)
	}
	if strings.Count(s, "\n") > 2 {
		t.Fatalf("expanded running tool should not produce noisy extra lines, got:\n%s", s)
	}
}

func TestToolEntry_InProgress(t *testing.T) {
	// In-progress entries display the verb form of the tool name
	// ("grep" → "Searching") so the line reads like a live status.
	e := ToolEntry{ToolName: "grep", Status: ToolStatusInProgress, Folded: true}
	s := stripAnsiCSI(renderToolEntry(e, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.Contains(s, "Searching") {
		t.Errorf("expected verb form 'Searching' for grep in progress, got: %q", s)
	}
}

func TestToolEntry_FocusedRender(t *testing.T) {
	e := ToolEntry{ToolName: "list_dir", Status: ToolStatusComplete, Folded: true}
	s := stripAnsiCSI(renderToolEntry(e, 80, true, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if !strings.Contains(s, "▶") {
		t.Errorf("focused render should have a ▶ focus indicator, got: %q", s)
	}
}

func TestToolEntry_UnfocusedRenderDiffers(t *testing.T) {
	e := ToolEntry{ToolName: "list_dir", Status: ToolStatusComplete, Folded: true}
	sFocused := stripAnsiCSI(renderToolEntry(e, 80, true, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	sUnfocused := stripAnsiCSI(renderToolEntry(e, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))))
	if sFocused == sUnfocused {
		t.Errorf("focused and unfocused should differ visually; both = %q", sFocused)
	}
}
