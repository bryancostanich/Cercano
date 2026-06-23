package banner

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// SweepDuration is how long the shimmer takes to cross the wordmark.
const SweepDuration = 1400 * time.Millisecond

// WaitDuration is the pause between consecutive sweeps. Total cycle is
// SweepDuration + WaitDuration.
const WaitDuration = 1000 * time.Millisecond

// TickInterval is the animation frame budget. 33ms ≈ 30fps — enough for a
// smooth perceived sweep without burning CPU during the sweep phase, and
// fast enough that the transition into/out of the wait phase reads as
// instantaneous.
const TickInterval = 33 * time.Millisecond

// cycleDuration is the full sweep + wait period.
const cycleDuration = SweepDuration + WaitDuration

// TickMsg is emitted by AnimModel's ticker to advance the animation.
type TickMsg time.Time

// AnimModel renders the banner with a recurring shimmer: a left-to-right
// sweep across the wordmark, followed by a brief wait, repeating until the
// caller stops forwarding ticks (typically when the splash is dismissed).
type AnimModel struct {
	Palette theme.Palette
	Meta    Meta

	started time.Time
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

// Update re-issues a tick on every TickMsg so the animation runs as long as
// the root model forwards ticks. To stop the animation, the caller gates the
// TickMsg forwarding (e.g. on a "splash dismissed" flag); the tick chain
// then dies naturally on the next message.
func (m AnimModel) Update(msg tea.Msg) (AnimModel, tea.Cmd) {
	if _, ok := msg.(TickMsg); !ok {
		return m, nil
	}
	return m, tickCmd()
}

// View renders the current frame. During the sweep portion of each cycle
// the bright band crosses the wordmark; during the wait portion the banner
// is rendered plain.
func (m AnimModel) View() string {
	phase := time.Since(m.started) % cycleDuration
	if phase >= SweepDuration {
		// Wait phase — plain banner.
		return Render(m.Palette, m.Meta)
	}
	progress := float64(phase) / float64(SweepDuration)
	const pad = 6.0
	sweepPos := -pad + progress*(float64(WordmarkCols)+2*pad)
	return RenderWithSweep(m.Palette, m.Meta, sweepPos)
}

func tickCmd() tea.Cmd {
	return tea.Tick(TickInterval, func(t time.Time) tea.Msg { return TickMsg(t) })
}
