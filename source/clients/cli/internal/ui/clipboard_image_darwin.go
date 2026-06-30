//go:build darwin

package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// clipboardImage returns a PNG from the macOS pasteboard if one is present.
// Tries `pngpaste` (Homebrew) first, then falls back to osascript exporting the
// clipboard image to a temp PNG.
func clipboardImage() ([]byte, string, bool) {
	if data, ok := pngpasteClipboard(); ok {
		return data, "image/png", true
	}
	if data, ok := osascriptClipboard(); ok {
		return data, "image/png", true
	}
	return nil, "", false
}

func pngpasteClipboard() ([]byte, bool) {
	bin, err := exec.LookPath("pngpaste")
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-").Output() // "-" → write image to stdout
	if err != nil || len(out) == 0 {
		return nil, false
	}
	if sniffImageType(out) != "image/png" {
		return nil, false
	}
	return out, true
}

const clipboardExportScript = `on run
  set outPath to (POSIX path of (path to temporary items)) & "cercano-clipboard.png"
  try
    set theData to (the clipboard as «class PNGf»)
  on error
    return ""
  end try
  set fh to open for access (POSIX file outPath) with write permission
  set eof fh to 0
  write theData to fh
  close access fh
  return outPath
end run`

func osascriptClipboard() ([]byte, bool) {
	scriptPath := filepath.Join(os.TempDir(), "cercano-clip-export.applescript")
	if err := os.WriteFile(scriptPath, []byte(clipboardExportScript), 0o600); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", scriptPath).Output()
	if err != nil {
		return nil, false
	}
	pngPath := strings.TrimSpace(string(out))
	if pngPath == "" {
		return nil, false
	}
	defer os.Remove(pngPath)
	data, err := os.ReadFile(pngPath)
	if err != nil || len(data) == 0 || sniffImageType(data) != "image/png" {
		return nil, false
	}
	return data, true
}
