package ui

import (
	"strings"
	"testing"
)

// stripFooterAnsi reuses the package-level stripAnsiCSI helper defined in
// confirm_test.go (same package) so we can assert on plain-text content
// without matching escape sequences.

func driveStreamDone(t *testing.T, m *Model, tokIn, tokOut int, notice, model string) {
	t.Helper()
	m.applyTurnTelemetry(chatDoneMsg{
		tokIn:  tokIn,
		tokOut: tokOut,
		notice: notice,
		model:  model,
	})
}

// TestFooterReflectsLastTurn drives a scripted Done event and asserts the
// footer shows the expected token counts and cloud state.
func TestFooterReflectsLastTurn(t *testing.T) {
	m := newStreamTestModel()
	driveStreamDone(t, &m, 12, 34, "", "qwen3-coder")

	plain := stripAnsiCSI(m.renderStatus())

	if !strings.Contains(plain, "last turn 12↑/34↓") {
		t.Errorf("renderStatus missing last-turn token counts; got: %q", plain)
	}
	if !strings.Contains(plain, "cloud:") || !strings.Contains(plain, "ok") {
		t.Errorf("renderStatus missing cloud:ok; got: %q", plain)
	}
}

// TestFooterCloudNoneOnNotice drives a Done event with a notice string and
// asserts the footer shows cloud: NONE.
func TestFooterCloudNoneOnNotice(t *testing.T) {
	m := newStreamTestModel()
	driveStreamDone(t, &m, 5, 10, "cloud not configured", "qwen3-coder")

	plain := stripAnsiCSI(m.renderStatus())

	if !strings.Contains(plain, "cloud:") || !strings.Contains(plain, "NONE") {
		t.Errorf("renderStatus should show cloud: NONE on notice; got: %q", plain)
	}
}

// TestFooterHiddenBeforeFirstTurn asserts a fresh model (no turn yet) does
// not emit a "last turn" section.
func TestFooterHiddenBeforeFirstTurn(t *testing.T) {
	m := newStreamTestModel()
	plain := stripAnsiCSI(m.renderStatus())

	if strings.Contains(plain, "last turn") {
		t.Errorf("renderStatus should not show last-turn before any turn completes; got: %q", plain)
	}
}

func TestFooterDividersAreCompact(t *testing.T) {
	m := newStreamTestModel()
	m.width = 200
	m.modelMaxTokens = 1000
	m.cumIn = 500
	m.ctxRaw = 1000
	m.permissionMode = "permissive"
	m.workDirOverride = "/tmp/cercano-dev"
	driveStreamDone(t, &m, 12, 34, "", "qwen3-coder")

	plain := stripAnsiCSI(m.renderStatus())
	if strings.Contains(plain, " ·") || strings.Contains(plain, "· ") {
		t.Fatalf("footer dividers should have no surrounding spaces; got: %q", plain)
	}
	if strings.Count(plain, "·") < 4 {
		t.Fatalf("test setup did not render multiple dividers; got: %q", plain)
	}
}

// TestApplyTelemetry exercises applyTurnTelemetry directly, asserting that
// each footer field is set correctly from the event.
func TestApplyTelemetry(t *testing.T) {
	m := newStreamTestModel()

	t.Run("sets tokIn/tokOut/hadTurn/cloudState ok", func(t *testing.T) {
		m.applyTurnTelemetry(chatDoneMsg{tokIn: 100, tokOut: 200, notice: "", model: "my-model"})
		if m.tokIn != 100 {
			t.Errorf("tokIn = %d, want 100", m.tokIn)
		}
		if m.tokOut != 200 {
			t.Errorf("tokOut = %d, want 200", m.tokOut)
		}
		if !m.hadTurn {
			t.Error("hadTurn should be true after applyTurnTelemetry")
		}
		if m.cloudState != "ok" {
			t.Errorf("cloudState = %q, want \"ok\"", m.cloudState)
		}
		if m.lastModel != "my-model" {
			t.Errorf("lastModel = %q, want \"my-model\"", m.lastModel)
		}
	})

	t.Run("sets cloudState NONE on notice", func(t *testing.T) {
		m2 := newStreamTestModel()
		m2.applyTurnTelemetry(chatDoneMsg{notice: "cloud absent"})
		if m2.cloudState != "NONE" {
			t.Errorf("cloudState = %q, want \"NONE\"", m2.cloudState)
		}
	})

	t.Run("accumulates cumIn/cumOut across calls", func(t *testing.T) {
		m3 := newStreamTestModel()
		m3.applyTurnTelemetry(chatDoneMsg{tokIn: 10, tokOut: 20})
		m3.applyTurnTelemetry(chatDoneMsg{tokIn: 5, tokOut: 7})
		if m3.cumIn != 15 {
			t.Errorf("cumIn = %d, want 15", m3.cumIn)
		}
		if m3.cumOut != 27 {
			t.Errorf("cumOut = %d, want 27", m3.cumOut)
		}
	})

	t.Run("empty model string does not overwrite lastModel", func(t *testing.T) {
		m4 := newStreamTestModel()
		m4.lastModel = "original"
		m4.applyTurnTelemetry(chatDoneMsg{model: ""})
		if m4.lastModel != "original" {
			t.Errorf("lastModel overwritten to empty; got %q", m4.lastModel)
		}
	})
}
