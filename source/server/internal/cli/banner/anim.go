package banner

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cercano/source/server/internal/cli/theme"
)

// SweepDuration is how long the shimmer takes to cross the wordmark.
const SweepDuration = 1400 * time.Millisecond

// TickInterval is the animation frame budget. 33ms ≈ 30fps — enough for a
// smooth perceived sweep without burning CPU.
const TickInterval = 33 * time.Millisecond

// TickMsg is emitted by AnimModel's ticker to advance the sweep.
type TickMsg time.Time

// AnimModel renders the banner with a moving shimmer sweep over the wordmark.
// One-shot: starts at construction, finishes after SweepDuration, after which
// View returns the static banner. Caller decides when to retire the model.
type AnimModel struct {
	Palette theme.Palette
	Meta    Meta

	started  time.Time
	finished bool
}

// NewAnimModel constructs the animation, starting the timer at the call site.
// Caller should batch its Init() Cmd into the root model's Init.
func NewAnimModel(p theme.Palette, m Meta) AnimModel {
	return AnimModel{
		Palette: p,
		Meta:    m,
		started: time.Now(),
	}
}

// Init kicks off the first tick.
func (m AnimModel) Init() tea.Cmd { return tickCmd() }

// Update advances the animation. The caller must forward TickMsg messages to
// this Update for the sweep to progress.
func (m AnimModel) Update(msg tea.Msg) (AnimModel, tea.Cmd) {
	if _, ok := msg.(TickMsg); !ok {
		return m, nil
	}
	if time.Since(m.started) >= SweepDuration {
		m.finished = true
		return m, nil
	}
	return m, tickCmd()
}

// Done reports whether the sweep has finished. Caller can stop forwarding
// ticks and / or dismiss the splash once Done returns true.
func (m AnimModel) Done() bool { return m.finished }

// View renders the current frame. After Done() the static banner is returned.
func (m AnimModel) View() string {
	if m.finished {
		return Render(m.Palette, m.Meta)
	}
	progress := float64(time.Since(m.started)) / float64(SweepDuration)
	if progress > 1 {
		progress = 1
	}
	const pad = 6.0
	sweepPos := -pad + progress*(float64(WordmarkCols)+2*pad)
	return RenderWithSweep(m.Palette, m.Meta, sweepPos)
}

func tickCmd() tea.Cmd {
	return tea.Tick(TickInterval, func(t time.Time) tea.Msg { return TickMsg(t) })
}
