package ui

import (
	"cercano/source/clients/cli/internal/theme"
	"fmt"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"html"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Search coordinates are terminal cells, never ANSI byte offsets. Entry identity
// and occurrence number survive history prepends and changes in wrapping.
type searchSpan struct{ row, from, to int }
type conversationMatch struct {
	entry      *Entry
	occurrence int
	spans      []searchSpan
}
type searchGlyph struct {
	text     string
	span     searchSpan
	boundary bool
}
type searchProjection struct {
	lines  []string
	query  string
	spans  [][]searchSpan
	glyphs []searchGlyph
}

type searchPaint struct {
	match int
	span  searchSpan
}

type conversationSearch struct {
	traceID                       uint64
	observed                      []searchObservedMessage
	dirty, working, jumpWhenReady bool
	revision                      uint64
	paint                         map[int][]searchPaint
	cache                         map[*Entry]searchProjection
	input                         promptInput
	chat                          *chatView
	convID                        string
	matches                       []conversationMatch
	active                        int
	query                         string
}

func searchableEntry(e *Entry) bool {
	return e != nil && (e.Role == RoleUser || e.Role == RoleAssistant) && e.Tool == nil && e.Banner == nil && e.SubAgentStart == nil && e.Content != "" && (!e.Superseded || e.SupersededOpen)
}

// projectSearchLines excludes entry chrome, styles, padding and code/table rules.
// A wrapped row boundary can stand for a space or no character (a split word).
// Blank rows and table cell boundaries are barriers, not searchable prose.
func projectSearchLines(lines []string, role Role) []searchGlyph {
	var out []searchGlyph
	for row, styled := range lines {
		plain := ansi.Strip(styled)
		if role == RoleUser && row == 0 {
			plain = strings.TrimPrefix(plain, "▶ ")
			plain = "  " + plain
		}
		trimmed := strings.TrimSpace(plain)
		if trimmed == "" || (role == RoleAssistant && ((strings.HasPrefix(trimmed, "─") && strings.HasSuffix(trimmed, "─") && strings.Count(trimmed, "─") > 2) || (strings.HasPrefix(trimmed, "━") && strings.HasSuffix(trimmed, "━") && strings.Count(trimmed, "━") > 2))) {
			out = append(out, searchGlyph{text: "\x00"})
			continue
		}
		if row > 0 {
			out = append(out, searchGlyph{boundary: true, text: " "})
		}
		// Remove only renderer padding; retain spacing inside code and prose.
		last := len(strings.TrimRight(plain, " \t"))
		col := 0
		g := uniseg.NewGraphemes(plain)
		for g.Next() {
			start, _ := g.Positions()
			text, w := g.Str(), g.Width()
			from := col
			col += w
			if start >= last || col <= entryIndent {
				continue
			}
			if role == RoleAssistant && ((text == "│" && strings.HasPrefix(trimmed, "│")) || (text == "┃" && strings.HasPrefix(trimmed, "┃"))) {
				out = append(out, searchGlyph{text: "\x00"})
				continue
			}
			out = append(out, searchGlyph{text: text, span: searchSpan{row, from, col}})
		}
	}
	return out
}

func findSearchSpans(glyphs []searchGlyph, query string) [][]searchSpan {
	if query == "" {
		return nil
	}
	// Fold using EqualFold per grapheme, which supports Unicode simple folding
	// without using lowercase byte offsets as if they were screen columns.
	var needle []string
	q := uniseg.NewGraphemes(query)
	for q.Next() {
		needle = append(needle, q.Str())
	}
	var matches [][]searchSpan
	for start := 0; start < len(glyphs); start++ {
		if glyphs[start].boundary || glyphs[start].text == "\x00" {
			continue
		}
		var spans []searchSpan
		j, k := start, 0
		for j < len(glyphs) && k < len(needle) {
			g := glyphs[j]
			if g.boundary {
				if g.text != "" {
					if needle[k] != " " && needle[k] != "\n" {
						break
					}
					k++
				}
				j++
				continue
			}
			if !strings.EqualFold(g.text, needle[k]) {
				break
			}
			if len(spans) > 0 && spans[len(spans)-1].row == g.span.row && spans[len(spans)-1].to == g.span.from {
				spans[len(spans)-1].to = g.span.to
			} else {
				spans = append(spans, g.span)
			}
			j++
			k++
		}
		if k == len(needle) && len(spans) > 0 {
			matches = append(matches, spans)
			start = j - 1
		}
	}
	return matches
}

func (s *conversationSearch) rebuild() {
	trace := s.traceContext()
	defer trace.span("index")()
	var projectionTime, matchingTime time.Duration
	var hits, projected int
	defer func() {
		trace.record("projection.total", projectionTime, searchTraceDetails{CacheHits: hits, Projected: projected})
		trace.record("matching.total", matchingTime, searchTraceDetails{Matches: len(s.matches)})
	}()

	old := conversationMatch{}
	if s.active >= 0 && s.active < len(s.matches) {
		old = s.matches[s.active]
	}
	query := s.input.Value()
	changed := query != s.query
	s.query = query
	s.matches = nil
	s.paint = make(map[int][]searchPaint)
	s.active = 0
	if query == "" {
		s.cache = nil
		return
	}
	nextCache := make(map[*Entry]searchProjection)
	for _, unit := range s.chat.layout.units {
		if unit.kind != unitEntry || unit.startEntry < 0 || unit.startEntry >= len(s.chat.entries) {
			continue
		}
		entry := s.chat.entries[unit.startEntry]
		if !searchableEntry(entry) {
			continue
		}
		cached, ok := s.cache[entry]
		if !ok || !slices.Equal(cached.lines, unit.lines) {
			var started time.Time
			if trace.Session != 0 {
				started = time.Now()
			}
			cached = searchProjection{lines: slices.Clone(unit.lines), glyphs: projectMessageSearch(unit.lines, entry)}
			projected++
			if !started.IsZero() {
				elapsed := time.Since(started)
				projectionTime += elapsed
				if elapsed >= 20*time.Millisecond {
					trace.record("projection.slow_entry", elapsed, searchTraceDetails{Count: unit.startEntry, SourceBytes: len(entry.Content)})
				}
			}
		} else {
			hits++
		}
		if cached.query != query {
			cached.query = query
			var started time.Time
			if trace.Session != 0 {
				started = time.Now()
			}
			cached.spans = findSearchSpans(cached.glyphs, query)
			if !started.IsZero() {
				elapsed := time.Since(started)
				matchingTime += elapsed
				if elapsed >= 20*time.Millisecond {
					trace.record("matching.slow_entry", elapsed, searchTraceDetails{Count: unit.startEntry, Matches: len(cached.spans), SourceBytes: len(entry.Content)})
				}
			}
		}
		nextCache[entry] = cached
		for occurrence, localSpans := range cached.spans {
			spans := slices.Clone(localSpans)
			for i := range spans {
				spans[i].row += unit.startLine
			}
			s.matches = append(s.matches, conversationMatch{entry, occurrence, spans})
			for _, span := range spans {
				s.paint[span.row] = append(s.paint[span.row], searchPaint{len(s.matches) - 1, span})
			}
			if !changed && old.entry == entry && old.occurrence == occurrence {
				s.active = len(s.matches) - 1
			}
		}
	}
	s.cache = nextCache
}

func (s *conversationSearch) jump(delta int) {
	if len(s.matches) == 0 {
		return
	}
	s.active = (s.active + delta + len(s.matches)) % len(s.matches)
	row := s.matches[s.active].spans[0].row
	s.chat.SetYOffset(row)
}

func (c *chatView) renderSearchOnLine(line string, row int) string {
	if c.search == nil || len(c.search.paint[row]) == 0 {
		return line
	}
	// Matches are ordered, nonoverlapping terminal-cell spans. Always cut
	// from the original line: cutting an already-highlighted line replays
	// generated SGR sequences and makes repeated highlights grow exponentially.
	var out strings.Builder
	width := ansi.StringWidth(line)
	cursor := 0
	for _, paint := range c.search.paint[row] {
		from, to := max(cursor, paint.span.from), min(width, paint.span.to)
		if from >= to {
			continue
		}
		if cursor < from {
			out.WriteString(ansi.Cut(line, cursor, from))
		}
		sgr := "\x1b[4m"
		if paint.match == c.search.active {
			sgr = theme.SelectionBackgroundSGR(c.palette)
		}
		segment := ansi.Cut(line, from, to)
		out.WriteString(highlightRange(segment, 0, to-from, sgr))
		cursor = to
	}
	if cursor < width {
		out.WriteString(ansi.Cut(line, cursor, width))
	}
	return out.String()
}

func (m Model) searchVisible() bool {
	return m.search != nil && !m.contentPageActive() && m.search.chat == m.activeChat() && m.search.convID == m.convID
}
func (m Model) searchCanOwnInput() bool {
	return !m.contentPageActive() && m.openRuntimeModal == nil && m.chatgptLoginModal == nil && m.claudeLoginModal == nil
}

func (m *Model) openConversationSearch(query string) {
	if !m.searchCanOwnInput() {
		return
	}
	if m.search == nil || m.search.chat != m.activeChat() || m.search.convID != m.convID {
		m.closeConversationSearch()
		input := newPromptInput()
		input.MinHeight = 1
		input.MaxHeight = 1
		input.SetPromptFunc(6, func(promptInfo) string { return "Find: " })
		input.Placeholder = "Search messages"
		input.Focus()
		m.search = &conversationSearch{input: input, chat: m.activeChat(), convID: m.convID}
		m.activeChat().search = m.search
	}
	m.search.input.SetValue(query)
	m.relayout()
	m.search.invalidate(true)
}

func (m *Model) closeConversationSearch() {
	if m.search == nil {
		return
	}
	m.search.chat.search = nil
	m.search = nil
}

func (m *Model) updateConversationSearch() {
	if m.search == nil {
		return
	}
	if m.search.convID != m.convID || m.search.chat != m.activeChat() {
		m.closeConversationSearch()
		m.relayout()
		return
	}
	if m.search.observeLayout() {
		m.search.invalidate(false)
	}
	m.sizeSearchInput()
}

func (m Model) searchStatus() string {
	if !m.searchVisible() {
		return ""
	}
	status := "Type to search"
	if m.search.input.Value() != "" {
		status = "No matches"
		if m.search.dirty || m.search.working {
			status = "Searching…"
		}
		if n := len(m.search.matches); n > 0 {
			status = fmt.Sprintf("%d of %d", m.search.active+1, n)
		}
	}
	if m.resumeHydrating || m.resumeBackfilling || m.search.chat.progressiveOlderLoadingIndex() >= 0 {
		status += " · Loading history…"
	}
	return status
}

func (m Model) searchRowStatus() string {
	status := m.searchStatus()
	width := m.mainContentWidth()
	if ansi.StringWidth(status) > width/2 {
		if strings.Contains(status, "Loading history…") {
			status = "Loading…"
		} else {
			status = ansi.Truncate(status, maxInt(8, width/2), "…")
		}
	}
	if width >= 110 {
		status += " · Enter next · Shift+Enter previous · Esc close"
	}
	return status
}
func (m *Model) sizeSearchInput() {
	if m.searchVisible() {
		m.search.input.SetWidth(maxInt(8, m.mainContentWidth()-ansi.StringWidth(m.searchRowStatus())-1))
	}
}
func (m Model) mainContentWidth() int { return maxInt(20, m.width-m.taskPaneWidth()) }
func (m Model) renderConversationSearch() string {
	width := m.mainContentWidth()
	status := m.searchRowStatus()
	fieldW := maxInt(1, width-ansi.StringWidth(status)-1)
	field := ansi.Truncate(m.search.input.View(), fieldW, "")
	return padToWidth(field, fieldW) + " " + m.styles.Muted.Render(status)
}

func searchInputText(text string) string {
	// Search is a single-line editor. Never turn bracketed paste into commands.
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}
func (m Model) handleConversationSearch(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	if !m.searchCanOwnInput() {
		return m, nil, false
	}
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "ctrl+f" {
		if !m.searchVisible() {
			m.openConversationSearch("")
		}
		return m, nil, true
	}
	if !m.searchVisible() {
		return m, nil, false
	}
	switch event := msg.(type) {
	case tea.MouseClickMsg:
		mouse := event.Mouse()
		if mouse.Button == tea.MouseLeft && m.mouseInPrompt(mouse) {
			// Hit-test against the frame the user clicked, before removing
			// the search row changes the layout.
			row := mouse.Y - m.promptTop()
			m.closeConversationSearch()
			m.relayout()
			m.activeChat().ClearSelection()
			cmd := m.input.Focus()
			m.input.MouseDown(mouse.X, row)
			return m, cmd, true
		}
	case tea.KeyPressMsg:
		if event.Key().Code == tea.KeyEscape {
			m.closeConversationSearch()
			m.relayout()
			return m, nil, true
		}
		if event.Key().Code == tea.KeyEnter {
			delta := 1
			if event.Key().Mod.Contains(tea.ModShift) {
				delta = -1
			}
			m.search.jump(delta)
			return m, nil, true
		}
		switch event.String() {
		case "pgup", "pgdown", "ctrl+b", "ctrl+u", "ctrl+d":
			return m, m.activeChat().Update(event), true
		}
		before := m.search.input.Value()
		var cmd tea.Cmd
		m.search.input, cmd = m.search.input.Update(event)
		if m.search.input.Value() != before {
			m.search.invalidate(true)
			m.sizeSearchInput()
		}
		return m, cmd, true
	case tea.PasteMsg:
		m.search.input.InsertString(searchInputText(event.Content))
		m.search.invalidate(true)
		m.sizeSearchInput()
		return m, nil, true
	}
	return m, nil, false
}

// readableSearchSource is used only to recover whitespace lost by terminal
// wrapping, not to match hidden Markdown syntax or derive screen offsets.
func readableSearchSource(e *Entry) string {
	if e.Role == RoleUser {
		return e.Content
	}
	source := []byte(e.Content)
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))
	var out strings.Builder
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if n.Type() == ast.TypeBlock {
				out.WriteByte('\n')
			}
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Text:
			value := string(node.Segment.Value(source))
			if node.Parent() != nil && node.Parent().Kind() != ast.KindCodeSpan {
				value = html.UnescapeString(value)
			}
			out.WriteString(value)
			if node.SoftLineBreak() || node.HardLineBreak() {
				out.WriteByte(' ')
			}
		case *ast.String:
			out.Write(node.Value)
		case *ast.AutoLink:
			out.Write(node.Label(source))
		case *ast.FencedCodeBlock:
			for i := 0; i < node.Lines().Len(); i++ {
				line := node.Lines().At(i)
				out.Write(line.Value(source))
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			for i := 0; i < node.Lines().Len(); i++ {
				line := node.Lines().At(i)
				out.Write(line.Value(source))
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return out.String()
}

func projectMessageSearch(lines []string, e *Entry) []searchGlyph {
	if e.Streaming && len(lines) > 0 {
		lines = slices.Clone(lines)
		last := len(lines) - 1
		lines[last] = strings.TrimSuffix(strings.TrimRight(ansi.Strip(lines[last]), " "), " ⟳")
	}
	glyphs := projectSearchLines(lines, e.Role)
	readable := readableSearchSource(e)
	offset := 0
	pending := -1
	for i := range glyphs {
		g := &glyphs[i]
		if g.boundary {
			pending = i
			continue
		}
		if g.text == "\x00" {
			pending = -1
			continue
		}
		at := strings.Index(readable[offset:], g.text)
		if at < 0 {
			continue
		} // renderer-owned decoration has no source position
		if pending >= 0 {
			gap := readable[offset : offset+at]
			// Only a directly adjacent character is a split word. All other
			// boundaries retain a separator, avoiding concatenated-word false hits.
			if gap == "" {
				glyphs[pending].text = ""
			}
			pending = -1
		}
		offset += at + len(g.text)
	}
	return glyphs
}
