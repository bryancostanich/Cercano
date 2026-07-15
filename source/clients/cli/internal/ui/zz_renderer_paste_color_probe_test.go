package ui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestRendererPasteColorProbe drives the REAL ultraviolet TerminalRenderer
// (the exact component bubbletea's cursedRenderer uses) across a frame
// transition that mimics pasting text into an already-rendered, colored
// prompt line. It captures the emitted diff bytes for the paste frame and
// checks whether any printable character on the colored line is emitted while
// the terminal pen is at the DEFAULT color -- which is the "leading cells lose
// their color" corruption we observed in the live app trace.
//
// This is a probe, not an assertion of desired behavior yet: it prints the
// emitted sequence and reports whether the color-loss pattern reproduces, so
// we can pin the bug precisely at the renderer boundary.
func TestRendererPasteColorProbe(t *testing.T) {
	const (
		width  = 40
		height = 6
		orange = "\x1b[38;2;234;130;18m" // #ea8212, the prompt Text color
		reset  = "\x1b[m"
	)

	var buf bytes.Buffer
	r := uv.NewTerminalRenderer(&buf, []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor", // force true color so the orange is preserved verbatim
	})
	r.SetRelativeCursor(true)
	r.SetScrollOptim(true)
	// The renderer auto-detected a profile from the bytes.Buffer (non-TTY).
	// Force TrueColor so downsampling can't strip color -- this isolates
	// whether the loss is a diff bug vs a profile artifact.
	r.SetColorProfile(colorprofile.TrueColor)

	scr := uv.NewScreenBuffer(width, height)

	// Frame 1: an already-typed, colored prompt prefix on row 0.
	// This is what's on screen before the paste.
	prefix := "recap of what happened. i tried to "
	frame1 := orange + prefix + reset
	uv.NewStyledString(frame1).Draw(scr, scr.Bounds())
	r.Render(scr.RenderBuffer)
	if err := r.Flush(); err != nil {
		t.Fatalf("frame1 flush: %v", err)
	}
	t.Logf("frame-1 emitted bytes: %q", buf.String())
	buf.Reset() // discard frame-1 bytes; we only care about the paste diff

	// Frame 2: the same colored prefix, now with pasted text appended that
	// wraps onto a second visual row. The prefix cells are IDENTICAL to
	// frame 1, the tail cells are new -- this is the exact condition that
	// pushes the diff engine into the EraseLine + skip-equal-prefix path.
	pasted := "recap of what happened. i tried to paste `* single ' apostrophe`"
	frame2 := orange + pasted + reset
	uv.NewStyledString(frame2).Draw(scr, scr.Bounds())
	r.Render(scr.RenderBuffer)
	if err := r.Flush(); err != nil {
		t.Fatalf("frame2 flush: %v", err)
	}

	out := buf.String()
	t.Logf("paste-frame emitted bytes: %q", out)

	// Walk the emitted stream and track pen color. Any printable run emitted
	// while the pen is default-colored is a color-loss occurrence.
	lost := scanForUncoloredPrintable(out, orange, reset)
	if lost != "" {
		t.Logf("REPRODUCED: printable text emitted with DEFAULT pen (color lost): %q", lost)
	} else {
		t.Logf("not reproduced at renderer boundary: all printable runs carry the orange pen")
	}
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// scanForUncoloredPrintable walks the byte stream, tracking whether the pen is
// currently the orange color. It returns the first run of printable characters
// (letters/digits/punct that belong to the pasted content) emitted while the
// pen is NOT orange, or "" if none. Cursor-movement and erase control
// sequences are skipped without affecting the pen.
func scanForUncoloredPrintable(out, orange, reset string) string {
	penOrange := false
	i := 0
	for i < len(out) {
		if out[i] == 0x1b {
			// Consume the whole escape sequence.
			loc := escSeqLen(out[i:])
			seq := out[i : i+loc]
			switch {
			case seq == orange:
				penOrange = true
			case seq == reset || isResetSGR(seq):
				penOrange = false
			case sgrRe.MatchString(seq):
				// Some other SGR: if it sets a foreground color, treat as colored.
				if strings.Contains(seq, "38;") {
					penOrange = true
				}
			}
			i += loc
			continue
		}
		c := out[i]
		if c >= 0x20 && c < 0x7f { // printable ASCII
			if !penOrange {
				// Collect the uncolored printable run.
				j := i
				for j < len(out) && out[j] >= 0x20 && out[j] < 0x7f {
					j++
				}
				return out[i:j]
			}
		}
		i++
	}
	return ""
}

// escSeqLen returns the length of the ANSI escape sequence at the start of s.
func escSeqLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 1
	}
	if s[1] == '[' {
		// CSI: ESC [ ... final byte in 0x40-0x7e
		for k := 2; k < len(s); k++ {
			if s[k] >= 0x40 && s[k] <= 0x7e {
				return k + 1
			}
		}
		return len(s)
	}
	if s[1] == ']' {
		// OSC: ESC ] ... BEL or ST
		for k := 2; k < len(s); k++ {
			if s[k] == 0x07 {
				return k + 1
			}
			if s[k] == 0x1b && k+1 < len(s) && s[k+1] == '\\' {
				return k + 2
			}
		}
		return len(s)
	}
	return 2
}

func isResetSGR(seq string) bool {
	return seq == "\x1b[0m" || seq == "\x1b[m"
}
