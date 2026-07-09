package ui

import (
	"fmt"
	"strings"
	"time"
)

// turnstatus.go builds the live status the footer shows while a turn is in
// flight: "<activity> · <elapsed> · <N tok↑> · <model> (local|cloud)". These
// are pure helpers; renderStatus adds the spinner, styling, and interrupt hint.

// formatElapsed renders a turn's running time in whole seconds. Under a minute
// it stays compact ("4s"); at a minute or more it becomes easier to scan
// ("1m05s"). Whole seconds keep the timer from flickering on every frame.
func formatElapsed(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

// turnStatusLine composes the live status content. The token count is shown
// only once output has started, and the engine badge only once the route is
// known.
func turnStatusLine(activity string, elapsed time.Duration, tokOut int, model string, isCloud bool) string {
	parts := []string{activity, formatElapsed(elapsed)}
	if tokOut > 0 {
		parts = append(parts, fmt.Sprintf("%d tok↑", tokOut))
	}
	if model != "" {
		loc := "local"
		if isCloud {
			loc = "cloud"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", model, loc))
	}
	return strings.Join(parts, " · ")
}
