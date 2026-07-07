package ui

import (
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"
)

// openBrowserURL opens url in the user's default browser (best-effort). macOS
// `open`, Windows rundll32, everything else `xdg-open`. Returns the launch
// error; callers generally ignore it — a failed open just leaves the user to
// click or type the URL themselves.
func openBrowserURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// openBrowserCmd opens url in the background as a tea.Cmd. Errors are swallowed
// — the modal keeps showing the URL as a fallback.
func openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		_ = openBrowserURL(url)
		return nil
	}
}

// hyperlink wraps label in an OSC 8 terminal hyperlink pointing at url, so
// supporting terminals render it clickable. Terminals without OSC 8 just show
// label — the escape sequences are zero-width, and x/ansi width helpers treat
// them as such, so box layout is unaffected.
func hyperlink(url, label string) string {
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}
