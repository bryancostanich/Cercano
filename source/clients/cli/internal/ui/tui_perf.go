package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

const defaultTUIPerfSlowThreshold = 80 * time.Millisecond

var tuiPerfLogMu sync.Mutex

// tuiPerfEnabled keeps the probe invisible but always available. It logs only
// slow UI operations by default, so rare intermittent stalls leave evidence even
// when the user did not pre-enable a debug flag. Set CERCANO_TUI_PERF=0 to
// disable, CERCANO_TUI_PERF_MS to tune the slow threshold, or
// CERCANO_TUI_PERF_LOG to redirect the file.
func tuiPerfEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("CERCANO_TUI_PERF")))
	return v != "0" && v != "false" && v != "off"
}

func tuiPerfSlowThreshold() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("CERCANO_TUI_PERF_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultTUIPerfSlowThreshold
}

func tuiPerfLogPath() string {
	if p := strings.TrimSpace(os.Getenv("CERCANO_TUI_PERF_LOG")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "cercano-tui-perf.log"
	}
	return filepath.Join(home, ".config", "cercano", "tui-perf.log")
}

func (m Model) logSlowUpdate(start time.Time, msg tea.Msg) {
	elapsed := time.Since(start)
	if elapsed < tuiPerfSlowThreshold() || !tuiPerfEnabled() {
		return
	}
	m.appendTUIPerf("update", elapsed, fmt.Sprintf("msg=%T", msg))
}

func (m Model) logSlowView(start time.Time) {
	elapsed := time.Since(start)
	if elapsed < tuiPerfSlowThreshold() || !tuiPerfEnabled() {
		return
	}
	m.appendTUIPerf("view", elapsed, "")
}

func (m Model) logSlowRefreshViewport(start time.Time) {
	elapsed := time.Since(start)
	if elapsed < tuiPerfSlowThreshold() || !tuiPerfEnabled() {
		return
	}
	m.appendTUIPerf("refreshViewport", elapsed, "")
}

func (m Model) appendTUIPerf(op string, elapsed time.Duration, extra string) {
	path := tuiPerfLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	active := ""
	if len(m.chatTabs.tabs) > 0 {
		active = m.chatTabs.active
	}
	main := m.mainChat()
	activeChat := m.activeChat()
	line := fmt.Sprintf(
		"%s op=%s elapsed_ms=%.1f %s active_tab=%q width=%d height=%d streaming=%v anim=%v chat_dirty=%v compacting=%v main_in_progress=%v main_loading=%v main_between=%v active_in_progress=%v active_loading=%v active_between=%v main_entries=%d active_entries=%d total_lines=%d yoff=%d\n",
		time.Now().Format(time.RFC3339Nano),
		op,
		float64(elapsed.Microseconds())/1000.0,
		extra,
		active,
		m.width,
		m.height,
		m.streaming,
		m.animTickActive,
		m.chatDirty,
		m.compacting,
		main.hasInProgressTool(),
		main.hasLoadingTool(),
		main.IsBetweenPhases(),
		activeChat.hasInProgressTool(),
		activeChat.hasLoadingTool(),
		activeChat.IsBetweenPhases(),
		len(main.entries),
		len(activeChat.entries),
		activeChat.TotalLineCount(),
		activeChat.YOffset(),
	)

	tuiPerfLogMu.Lock()
	defer tuiPerfLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}
