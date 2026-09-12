package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
)

// At most one worker belongs to each search session. Typing and history updates
// coalesce while it runs; closing/reopening search invalidates the owner token.
type conversationSearchResultMsg struct {
	completedAt time.Time
	trace       searchTraceContext
	owner       *conversationSearch
	revision    uint64
	query       string
	matches     []conversationMatch
	cache       map[*Entry]searchProjection
	starts      map[*Entry]int
}

func (s *conversationSearch) invalidate(jump bool) {
	s.revision++
	reason := "layout"
	if jump {
		reason = "input"
	}
	s.traceContext().record("invalidate", 0, searchTraceDetails{Status: reason})
	s.dirty = true
	s.jumpWhenReady = s.jumpWhenReady || jump
	if s.query != s.input.Value() {
		s.matches = nil
		s.paint = nil
		s.active = 0
	}
	if s.input.Value() == "" {
		s.query = ""
		s.matches = nil
		s.paint = nil
		s.dirty = false
		s.jumpWhenReady = false
	}
}

func (m *Model) conversationSearchCmd() tea.Cmd {
	s := m.search
	if s == nil || !m.searchVisible() || !s.dirty || s.working {
		return nil
	}
	trace := s.traceContext()
	defer trace.span("snapshot")()
	s.working = true
	s.dirty = false
	query, revision := s.input.Value(), s.revision
	// Entry values and line slices are copied before crossing the goroutine
	// boundary. Workers never read live entries, layouts, editors, or cache maps.
	chat := &chatView{}
	chat.layout.totalLines = s.chat.layout.totalLines
	chat.entries = make([]*Entry, len(s.chat.entries))
	originals := make(map[*Entry]*Entry, len(chat.entries))
	cache := make(map[*Entry]searchProjection, len(s.cache))
	for i, original := range s.chat.entries {
		if !searchableEntry(original) {
			continue
		}
		entry := *original
		chat.entries[i] = &entry
		originals[&entry] = original
		if cached, ok := s.cache[original]; ok {
			cache[&entry] = cached
		}
	}
	starts := make(map[*Entry]int, len(chat.entries))
	for _, unit := range s.chat.layout.units {
		if unit.kind != unitEntry || unit.startEntry < 0 || unit.startEntry >= len(chat.entries) || chat.entries[unit.startEntry] == nil {
			continue
		}
		unit.lines = slices.Clone(unit.lines)
		chat.layout.units = append(chat.layout.units, unit)
		starts[s.chat.entries[unit.startEntry]] = unit.startLine
	}
	scheduledAt := time.Now()
	return func() tea.Msg {
		trace.record("worker.queue", time.Since(scheduledAt), searchTraceDetails{})
		defer trace.span("worker")()
		input := newPromptInput()
		input.SetValue(query)
		worker := conversationSearch{input: input, chat: chat, cache: cache, traceID: trace.Session, revision: revision}
		worker.rebuild()
		result := conversationSearchResultMsg{owner: s, revision: revision, query: query, matches: worker.matches, cache: make(map[*Entry]searchProjection, len(worker.cache)), starts: starts}
		for i := range result.matches {
			result.matches[i].entry = originals[result.matches[i].entry]
		}
		for entry, cached := range worker.cache {
			result.cache[originals[entry]] = cached
		}
		result.completedAt = time.Now()
		result.trace = trace
		trace.record("worker.results", 0, searchTraceDetails{Matches: len(result.matches), Count: len(result.cache)})
		return result
	}
}

func (m *Model) applyConversationSearchResult(result conversationSearchResultMsg) {
	trace := result.trace
	defer trace.span("apply")()
	if !result.completedAt.IsZero() {
		trace.record("result.queue", time.Since(result.completedAt), searchTraceDetails{})
	}
	s := m.search
	if s == nil || s != result.owner {
		trace.record("result.discard", 0, searchTraceDetails{Status: "closed_or_replaced"})
		return
	}
	s.working = false
	// Even an old query's immutable projections are reusable. Content changes
	// are checked against rendered lines before reuse by the next worker.
	s.cache = result.cache
	if result.query != s.input.Value() {
		trace.record("result.discard", 0, searchTraceDetails{Status: "superseded_query"})
		return
	}
	old := conversationMatch{}
	if s.active >= 0 && s.active < len(s.matches) {
		old = s.matches[s.active]
	}
	s.query = result.query
	s.matches = nil
	s.paint = make(map[int][]searchPaint)
	s.active = 0
	// History can arrive while a worker runs. Rebase unchanged messages to their
	// current rows; don't paint stale spans over a changed or removed message.
	current := make(map[*Entry]renderUnit)
	for _, unit := range s.chat.layout.units {
		if unit.kind != unitEntry || unit.startEntry < 0 || unit.startEntry >= len(s.chat.entries) {
			continue
		}
		entry := s.chat.entries[unit.startEntry]
		cached, cachedOK := result.cache[entry]
		if searchableEntry(entry) && cachedOK && slices.Equal(unit.lines, cached.lines) {
			current[entry] = unit
		}
	}
	for _, match := range result.matches {
		unit, ok := current[match.entry]
		if !ok {
			continue
		}
		delta := unit.startLine - result.starts[match.entry]
		for i := range match.spans {
			match.spans[i].row += delta
		}
		s.matches = append(s.matches, match)
		index := len(s.matches) - 1
		for _, span := range match.spans {
			s.paint[span.row] = append(s.paint[span.row], searchPaint{index, span})
		}
		if match.entry == old.entry && match.occurrence == old.occurrence {
			s.active = index
		}
	}
	if s.jumpWhenReady && len(s.matches) > 0 {
		s.jump(0)
		s.jumpWhenReady = false
	}
	if result.revision != s.revision {
		s.dirty = true
	}
	m.sizeSearchInput()
	trace.record("result.applied", 0, searchTraceDetails{Matches: len(s.matches)})
}

// Keep invalidation proportional to message metadata, without allocating or
// re-indexing the transcript on animation ticks or unrelated tool updates.
type searchObservedMessage struct {
	entry      *Entry
	content    string
	row, width int
}

func (s *conversationSearch) observeLayout() bool {
	oldLen := len(s.observed)
	next := s.observed[:0]
	changed := false
	for _, unit := range s.chat.layout.units {
		if unit.kind != unitEntry || unit.startEntry < 0 || unit.startEntry >= len(s.chat.entries) {
			continue
		}
		entry := s.chat.entries[unit.startEntry]
		if !searchableEntry(entry) {
			continue
		}
		value := searchObservedMessage{entry, entry.Content, unit.startLine, s.chat.Width()}
		i := len(next)
		if i >= oldLen || s.observed[i] != value {
			changed = true
		}
		next = append(next, value)
	}
	changed = changed || len(next) != oldLen
	s.observed = next
	return changed
}
