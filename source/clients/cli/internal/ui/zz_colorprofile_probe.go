package ui

// TEMPORARY INSTRUMENTATION — remove once the paste color-loss bug is diagnosed.
//
// This logs the color-profile lifecycle to a file so we can see, in a real
// terminal, (1) what colorprofile.Detect returns at startup and (2) each
// ColorProfileMsg bubbletea sends after its async capability query. That tells
// us whether the leading-segment color loss is a startup ANSI256->TrueColor
// downsample/transition or a NoTTY/ASCII detection.
//
// Log destination: $CERCANO_COLORPROBE_LOG, or ~/.config/cercano/colorprobe.log.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
)

// LogStartupColorProfile records what colorprofile.Detect returns for the real
// stdout+environment at launch, alongside the relevant env vars. Call this from
// main() before tea.NewProgram runs. Exported so the probe can be driven from
// the command package.
func LogStartupColorProfile() {
	detected := colorprofile.Detect(os.Stdout, os.Environ())
	colorProbeLog("=== STARTUP === Detect(os.Stdout)=%s TERM=%q COLORTERM=%q NO_COLOR=%q CLICOLOR=%q CLICOLOR_FORCE=%q",
		detected,
		os.Getenv("TERM"),
		os.Getenv("COLORTERM"),
		os.Getenv("NO_COLOR"),
		os.Getenv("CLICOLOR"),
		os.Getenv("CLICOLOR_FORCE"),
	)
}

// LogColorProfileMsg records a ColorProfileMsg observed in Update. Pass the
// profile carried by the message.
func LogColorProfileMsg(p colorprofile.Profile) {
	colorProbeLog("ColorProfileMsg -> %s", p)
}

var (
	colorProbeMu   sync.Mutex
	colorProbeOnce sync.Once
	colorProbePath string
)

func colorProbeLogPath() string {
	colorProbeOnce.Do(func() {
		if p := os.Getenv("CERCANO_COLORPROBE_LOG"); p != "" {
			colorProbePath = p
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			colorProbePath = "cercano-colorprobe.log"
			return
		}
		colorProbePath = filepath.Join(home, ".config", "cercano", "colorprobe.log")
	})
	return colorProbePath
}

// colorProbeLog appends a timestamped line to the probe log. Best-effort: any
// error is silently ignored so instrumentation never disturbs the TUI.
func colorProbeLog(format string, args ...any) {
	colorProbeMu.Lock()
	defer colorProbeMu.Unlock()
	f, err := os.OpenFile(colorProbeLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{ts}, args...)...)
}
