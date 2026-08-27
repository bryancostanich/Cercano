package ui

import (
	"strconv"
	"strings"

	"cercano/source/clients/cli/internal/render"
)

// This file implements the scrollback render cache. SetEntries used to
// re-render every entry on every rebuild, making rebuild cost O(transcript) —
// paid on every stream event and every 50ms animation frame, inside the
// single-threaded Update loop. But entries are append-only and almost all of
// them are frozen history; only the streaming assistant entry, tool groups
// with an in-flight spinner, the nav-focused group, and the shimmering banner
// are actually dynamic. Frozen entries render once and are served from cache
// until their content, the wrap width, or the theme generation changes.

// entryCacheKey captures every input that affects a frozen entry's rendered
// block. Comparable; string equality is O(1) for an unchanged string (same
// length + backing array short-circuits).
type entryCacheKey struct {
	width          int
	stylesGen      int
	role           Role
	content        string
	status         string
	superseded     bool
	supersededOpen bool
}

type entryRenderCache struct {
	key      entryCacheKey
	rendered string
	lines    []string
}

// groupCacheKey captures every input that affects a frozen tool-group block.
type groupCacheKey struct {
	width     int
	stylesGen int
	count     int
	expanded  bool
	fp        string
}

type groupRenderCache struct {
	key   groupCacheKey
	block string
	lines []string
	rows  []toolArrowRow
}

// transcriptPrefixCache stores the already-assembled render of the frozen
// prefix of the transcript. Per-entry caches avoid markdown rerendering; this
// cache avoids copying every frozen block into a fresh giant string on each
// streaming repaint. It is keyed by width, theme generation, and contentGen,
// and always ends at an entry boundary.
type transcriptPrefixCache struct {
	width      int
	stylesGen  int
	contentGen int
	end        int
	content    string
	arrowRows  []arrowRow
	lineCount  int
}

func (c *chatView) markTranscriptDirty() {
	c.contentGen++
}

// renderEntryCached returns renderEntry output, served from the per-entry
// cache when the entry is frozen. Dynamic entries bypass the cache: the
// banner (wall-clock shimmer), the streaming assistant entry (live tail +
// animated status line), and tool rows (cached at group level instead).
func (c *chatView) renderEntryCached(e *Entry, idx int) string {
	if e.Banner != nil || e.Tool != nil || e.Streaming || e.SubAgentStart != nil {
		return c.renderEntry(e, idx)
	}
	key := entryCacheKey{
		width:          c.Width(),
		stylesGen:      c.stylesGen,
		role:           e.Role,
		content:        e.Content,
		status:         e.Status,
		superseded:     e.Superseded,
		supersededOpen: e.SupersededOpen,
	}
	if cached, ok := c.entryCache[e]; ok && cached.key == key {
		return cached.rendered
	}
	out := c.renderEntry(e, idx)
	if c.entryCache == nil { // zero-value chatView (tests build it literally)
		c.entryCache = map[*Entry]entryRenderCache{}
	}
	c.entryCache[e] = entryRenderCache{key: key, rendered: out}
	return out
}

// renderEntryCachedLines returns renderEntry output as pre-split lines, served
// from the per-entry cache when the entry is frozen. Dynamic entries bypass the
// cache: the banner (wall-clock shimmer), the streaming assistant entry (live
// tail + animated status line), and tool rows (cached at group level instead).
func (c *chatView) renderEntryCachedLines(e *Entry, idx int) (string, []string) {
	if e.Banner != nil || e.Tool != nil || e.Streaming || e.SubAgentStart != nil {
		out := c.renderEntry(e, idx)
		return out, splitRenderLines(out)
	}
	key := entryCacheKey{
		width:          c.Width(),
		stylesGen:      c.stylesGen,
		role:           e.Role,
		content:        e.Content,
		status:         e.Status,
		superseded:     e.Superseded,
		supersededOpen: e.SupersededOpen,
	}
	if cached, ok := c.entryCache[e]; ok && cached.key == key {
		if cached.lines == nil && cached.rendered != "" {
			cached.lines = splitRenderLines(cached.rendered)
			c.entryCache[e] = cached
		}
		return cached.rendered, cached.lines
	}
	out := c.renderEntry(e, idx)
	lines := splitRenderLines(out)
	if c.entryCache == nil { // zero-value chatView (tests build it literally)
		c.entryCache = map[*Entry]entryRenderCache{}
	}
	c.entryCache[e] = entryRenderCache{key: key, rendered: out, lines: lines}
	return out, lines
}

// groupIsDynamic reports whether any member of a tool run renders a
// wall-clock animation (in-flight spinner, lazy-load spinner) and therefore
// must repaint every frame.
func groupIsDynamic(run []*Entry) bool {
	for _, e := range run {
		if e.Tool.Status == ToolStatusInProgress || e.Tool.Loading {
			return true
		}
	}
	return false
}

// groupFingerprint folds every render-affecting member field into one string.
// Full bodies (FullArgs/FullResult) are folded by length: they are written
// once by the lazy fetch (whose Loading flip already changes the key) and
// never rewritten in place with same-length content.
func groupFingerprint(run []*Entry) string {
	var b strings.Builder
	for _, e := range run {
		t := e.Tool
		b.WriteString(t.ToolUseID)
		b.WriteByte(0)
		b.WriteString(t.ToolName)
		b.WriteByte(0)
		b.WriteString(strconv.Itoa(int(t.Status)))
		b.WriteByte(0)
		b.WriteString(t.ArgsSummary)
		b.WriteByte(0)
		b.WriteString(t.ResultSummary)
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(int64(t.Duration), 10))
		b.WriteByte(0)
		b.WriteString(strconv.Itoa(t.StartLine))
		if t.Folded {
			b.WriteByte(1)
		} else {
			b.WriteByte(2)
		}
		b.WriteString(strconv.Itoa(len(t.FullArgs)))
		b.WriteByte(0)
		b.WriteString(strconv.Itoa(len(t.FullResult)))
		b.WriteByte(0)
	}
	return b.String()
}

// renderToolGroupCached returns renderToolGroupBlock output, cached per group
// and keyed by the run's start index (stable: entries are append-only, and
// the one insert path — insertNoticeAboveLast — changes the fingerprint of
// any group it shifts, forcing a miss). Groups with an animated member or
// the nav focus bypass the cache.
func (c *chatView) renderToolGroupCached(run []*Entry, startIdx int) (string, []toolArrowRow) {
	focusedIn := c.focusedToolIdx >= startIdx && c.focusedToolIdx < startIdx+len(run)
	if focusedIn || groupIsDynamic(run) {
		return c.renderToolGroupBlock(run, startIdx)
	}
	key := groupCacheKey{
		width:     c.Width(),
		stylesGen: c.stylesGen,
		count:     len(run),
		expanded:  c.groupExpanded[startIdx],
		fp:        groupFingerprint(run),
	}
	if g, ok := c.groupCache[startIdx]; ok && g.key == key {
		return g.block, g.rows
	}
	block, rows := c.renderToolGroupBlock(run, startIdx)
	if c.groupCache == nil { // zero-value chatView (tests build it literally)
		c.groupCache = map[int]groupRenderCache{}
	}
	c.groupCache[startIdx] = groupRenderCache{key: key, block: block, rows: rows}
	return block, rows
}

// renderToolGroupCachedLines returns renderToolGroupBlock output as pre-split
// lines, cached per group and keyed by the run's start index. Groups with an
// animated member or the nav focus bypass the cache.
func (c *chatView) renderToolGroupCachedLines(run []*Entry, startIdx int) (string, []string, []toolArrowRow) {
	focusedIn := c.focusedToolIdx >= startIdx && c.focusedToolIdx < startIdx+len(run)
	if focusedIn || groupIsDynamic(run) {
		block, rows := c.renderToolGroupBlock(run, startIdx)
		return block, splitRenderLines(block), rows
	}
	key := groupCacheKey{
		width:     c.Width(),
		stylesGen: c.stylesGen,
		count:     len(run),
		expanded:  c.groupExpanded[startIdx],
		fp:        groupFingerprint(run),
	}
	if g, ok := c.groupCache[startIdx]; ok && g.key == key {
		if g.lines == nil && g.block != "" {
			g.lines = splitRenderLines(g.block)
			c.groupCache[startIdx] = g
		}
		return g.block, g.lines, g.rows
	}
	block, rows := c.renderToolGroupBlock(run, startIdx)
	lines := splitRenderLines(block)
	if c.groupCache == nil { // zero-value chatView (tests build it literally)
		c.groupCache = map[int]groupRenderCache{}
	}
	c.groupCache[startIdx] = groupRenderCache{key: key, block: block, lines: lines, rows: rows}
	return block, lines, rows
}

// flushRenderCaches drops every cached render. Called when the entries slice
// is replaced wholesale (/clear, resume) — pointer keys from the old slice
// would otherwise pin dead entries and stale group indexes.
func (c *chatView) flushRenderCaches() {
	c.entryCache = map[*Entry]entryRenderCache{}
	c.groupCache = map[int]groupRenderCache{}
	c.streamPrefix = streamPrefixCache{}
	c.contentGen++
}

// streamPrefixCache caches the joined render of the streaming entry's
// committed markdown blocks so each frame re-renders only the live tail.
// blockCount+lastRawLen guard the one instability in SplitBlocks: the final
// committed block can still extend while the buffer ends exactly at its edge
// (a pipe-table gaining rows).
type streamPrefixCache struct {
	entry      *Entry
	width      int
	stylesGen  int
	blockCount int
	lastRawLen int
	prefix     string
}

// committedPrefix returns the joined rendered committed blocks for e,
// rebuilding only when the block list actually changed. The rebuild itself is
// cheap for unchanged blocks (Markdown.Render is output-cached), so a single
// slot suffices — eviction by a non-streaming entry costs one cheap rebuild.
func (c *chatView) committedPrefix(e *Entry, blocks []render.MdBlock, textW int) string {
	lastLen := 0
	if n := len(blocks); n > 0 {
		lastLen = len(blocks[n-1].Raw)
	}
	sp := &c.streamPrefix
	if sp.entry == e && sp.width == textW && sp.stylesGen == c.stylesGen &&
		sp.blockCount == len(blocks) && sp.lastRawLen == lastLen {
		return sp.prefix
	}
	var parts []string
	for _, b := range blocks {
		s := c.renderMdBlock(b, textW)
		if isHeadingBlock(b) {
			// A blank line before a heading gives it breathing room — but not
			// when the heading is the very first thing in the reply.
			if len(parts) > 0 {
				s = "\n" + s
			}
			// And a blank line after it, so the heading isn't glued to the
			// paragraph it introduces. SplitBlocks makes the heading its own
			// block, so parts are joined by a single "\n" and the following
			// body would otherwise sit on the very next line. A trailing blank
			// left dangling at the very end of the reply is trimmed by the
			// caller (renderAssistantMarkdown) when no streaming tail follows.
			s += "\n"
		}
		parts = append(parts, s)
	}
	*sp = streamPrefixCache{
		entry:      e,
		width:      textW,
		stylesGen:  c.stylesGen,
		blockCount: len(blocks),
		lastRawLen: lastLen,
		prefix:     strings.Join(parts, "\n"),
	}
	return sp.prefix
}
