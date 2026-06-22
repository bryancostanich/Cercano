package render

import (
	"strings"
	"sync"

	glamour "charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
)

// Markdown renders markdown to themed ANSI. Glamour bakes the wrap width in at
// construction, so one TermRenderer is built and cached per width; resize is a
// cache miss. Render output is cached per (width, source) since committed blocks
// are stable across frames. The live tail uses RenderLive (uncached) because its
// source changes every frame.
type Markdown struct {
	style ansi.StyleConfig

	mu        sync.Mutex
	renderers map[int]*glamour.TermRenderer
	out       map[string]string
}

// NewMarkdown builds a renderer factory for the given style.
func NewMarkdown(style ansi.StyleConfig) *Markdown {
	return &Markdown{
		style:     style,
		renderers: map[int]*glamour.TermRenderer{},
		out:       map[string]string{},
	}
}

func (m *Markdown) renderer(width int) *glamour.TermRenderer {
	if r, ok := m.renderers[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(m.style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	m.renderers[width] = r
	return r
}

// render does the actual Glamour call. Returns md unchanged on any error so
// output is never lost. Trims the trailing newlines Glamour appends.
func (m *Markdown) render(md string, width int) string {
	if width < 1 {
		width = 1
	}
	r := m.renderer(width)
	if r == nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.Trim(out, "\n")
}

// Render renders md at the given wrap width, caching the result by (width, md).
func (m *Markdown) Render(md string, width int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := keyFor(width, md)
	if v, ok := m.out[key]; ok {
		return v
	}
	v := m.render(md, width)
	m.out[key] = v
	return v
}

// RenderLive renders md without caching — for the in-progress tail block whose
// source changes every frame.
func (m *Markdown) RenderLive(md string, width int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.render(md, width)
}

func keyFor(width int, md string) string {
	var b strings.Builder
	b.WriteString(itoa(width))
	b.WriteByte(0)
	b.WriteString(md)
	return b.String()
}
