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

// TestFooterReflectsCloudState drives a scripted Done event and asserts the
// footer shows cloud state. Per-turn token counts are deliberately absent:
// they were footer noise and were removed.
func TestFooterReflectsCloudState(t *testing.T) {
	m := newStreamTestModel()
	driveStreamDone(t, &m, 12, 34, "", "qwen3-coder")

	plain := stripAnsiCSI(m.renderStatus())

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

// TestFooterOmitsPerTurnAndMessageCounts guards the removal of two noisy
// footer segments: the per-turn "last turn N↑/N↓" counter and the "msg N"
// message-token badge. Both are checked after a completed turn, since that is
// when they used to appear.
func TestFooterOmitsPerTurnAndMessageCounts(t *testing.T) {
	m := newStreamTestModel()
	m.width = 200
	m.modelMaxTokens = 128000
	m.ctxMessageTokens = 44000
	m.ctxEstimatedRequest = 61392
	m.ctxRaw = 900000
	driveStreamDone(t, &m, 12, 34, "", "qwen3-coder")

	plain := stripAnsiCSI(m.renderStatus())

	if strings.Contains(plain, "last turn") {
		t.Errorf("footer should not show per-turn token counts; got: %q", plain)
	}
	if strings.Contains(plain, "msg ") {
		t.Errorf("footer should not show the msg token badge; got: %q", plain)
	}
	// The context meter itself must survive the removal.
	if !strings.Contains(plain, "ctx ") {
		t.Errorf("context meter should still render; got: %q", plain)
	}
}

func TestFooterDividersUseSingleSpacePadding(t *testing.T) {
	m := newStreamTestModel()
	m.width = 200
	m.modelMaxTokens = 1000
	m.cumIn = 500
	m.ctxRaw = 1000
	m.permissionMode = "permissive"
	m.workDirOverride = "/tmp/cercano-dev"
	driveStreamDone(t, &m, 12, 34, "", "qwen3-coder")

	plain := stripAnsiCSI(m.renderStatus())
	if strings.Contains(plain, "  ·") || strings.Contains(plain, "·  ") {
		t.Fatalf("footer dividers should use one space on each side; got: %q", plain)
	}
	if strings.Count(plain, " · ") < 4 {
		t.Fatalf("test setup did not render multiple padded dividers; got: %q", plain)
	}
}

// TestApplyTelemetry exercises applyTurnTelemetry directly, asserting that
// each footer field is set correctly from the event.
func TestApplyTelemetry(t *testing.T) {
	m := newStreamTestModel()

	t.Run("sets tokOut/cumulative/cloudState ok", func(t *testing.T) {
		m.applyTurnTelemetry(chatDoneMsg{tokIn: 100, tokOut: 200, notice: "", model: "my-model"})
		// tokOut still feeds the live turn-status line. Per-turn input is only
		// accumulated now; the "last turn N↑/N↓" footer segment was removed.
		if m.tokOut != 200 {
			t.Errorf("tokOut = %d, want 200", m.tokOut)
		}
		if m.cumIn != 100 {
			t.Errorf("cumIn = %d, want 100", m.cumIn)
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
