package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/banner"
	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// staleStreamThreshold is how long a streaming assistant text entry may go
// without a new token before IsBetweenPhases treats it as quiescent and lets
// the trailing "still working" line appear beneath it. On a healthy fast
// stream tokens arrive well inside this window, so the line never shows; it
// only surfaces once the model stalls between a finished prose segment and its
// next action (e.g. parsing the next tool_use). Tuned to sit above natural
// inter-token pauses but low enough that a stall doesn't read as frozen.
const staleStreamThreshold = 500 * time.Millisecond

// turnStatus holds the live streaming telemetry that renderEntry needs while a
// turn is in-flight. Grouped here so chatView can own it rather than Model.
type turnStatus struct {
	activity string
	start    time.Time
	tokOut   int
	model    string
	cloud    bool
}

// chatView owns the transcript viewport: the rendered content, the scroll
// surface, and all entry rendering. Model delegates to it for everything that
// was previously handled via m.viewport, m.viewportPlainLines, and m.md.
type chatView struct {
	styles  theme.Styles
	palette theme.Palette
	md      *render.Markdown

	// entries is the authoritative slice of scrollback entries. All host
	// append/read operations go through the mutation methods below.
	entries []*Entry
	// root and home are construction-time constants used by humanizeArgs/humanizeResult.
	root string
	home string
	// queued holds messages submitted while a turn streams (FIFO). The host
	// renders them above the prompt and unstages by reading the methods below.
	queued []queuedTurn

	vp viewport.Model
	// content is the assembled transcript from the last SetEntries.
	// plainLines derives from it lazily (getPlainLines): ansi.Strip over the
	// whole transcript is O(transcript) and only selection/copy consumes it,
	// so it is computed on demand instead of on every rebuild.
	content    string
	plainDirty bool
	plainLines []string
	// stylesGen increments on SetStyles; cached renders carry the generation
	// they were built under, so a theme switch invalidates every cache.
	stylesGen int
	// entryCache / groupCache / transcriptPrefix / streamPrefix serve frozen
	// renders so rebuild cost tracks the active entry, not the transcript
	// (chat_render_cache.go).
	entryCache       map[*Entry]entryRenderCache
	groupCache       map[int]groupRenderCache
	transcriptPrefix transcriptPrefixCache
	streamPrefix     streamPrefixCache
	// contentGen increments when already-rendered historical content can change
	// shape without a width/theme change (fold toggles, inserted notices, lazy
	// tool bodies, wholesale replacement). Dynamic tail mutations do not need to
	// bump it because the assembled-prefix cache never includes them.
	contentGen int
	// arrowRows maps absolute content lines (indexes into plainLines) to the
	// entries index whose fold arrow is drawn there. Rebuilt by SetEntries on
	// every render, so it can never drift from the drawn layout.
	arrowRows []arrowRow
	linkRows  []linkRow

	// pendingCensor is the assistant entry the most recent watchdog
	// challenge/block fired against. If the model rewrites (a fresh assistant
	// entry starts streaming), the entry folds away as Superseded; a
	// successful justify call clears the mark instead, leaving the reply
	// visible. Nil when no challenge is pending.
	pendingCensor  *Entry
	focusedToolIdx int
	// groupExpanded tracks which collapsed multi-entry tool-call groups have
	// been expanded by Enter on a focused group. Key is the entries-slice
	// index of the FIRST tool entry in the group's contiguous run; stable
	// for the session since entries are append-only.
	groupExpanded map[int]bool

	// pendingToolFetches holds tool_use_ids the user expanded whose full body
	// hasn't been fetched yet. The host drains this (TakePendingToolFetches)
	// after a fold toggle and dispatches the lazy GetToolCall fetches.
	pendingToolFetches []string

	// bannerEpoch is the shimmer start time for the scrollback banner entry,
	// handed off from the splash AnimModel so the sweep phase is continuous.
	bannerEpoch time.Time
	// resizeAnchor is the distance-from-bottom captured by SetSize before the
	// dimensions change. SetEntries consumes it so reflow restores the same
	// visual anchor (the line at the viewport's bottom stays at the bottom)
	// instead of preserving the raw YOffset, which drifts on reflow.
	resizeAnchor    int
	hasResizeAnchor bool
	// vpH is the viewport height as last set by SetSize. charm.land/bubbles/v2's
	// Height() getter returns 0 until SetHeight() is called (WithHeight in the
	// constructor does not seed it), so we track the value ourselves so SetSize
	// can compute the correct resize anchor before calling SetHeight.
	vpH int

	turn     turnStatus
	animTime time.Time
	// streaming mirrors the host's m.streaming so the chat can render a
	// trailing "still working" indicator between the moment the assistant
	// finishes writing/tool-running and the moment the next event arrives.
	// Without this, multi-step turns go dark visually once the first phase
	// completes.
	streaming bool
	// tailReserve is the row count claimed by the trailing activity block, and
	// tailReserveBaseRows is the natural transcript height when that claim was
	// made. Once the "still working" line appears, hiding it mid-turn leaves the
	// unfilled remainder blank; real streamed content consumes the reserve
	// row-for-row so bottom-pinned scrollback does not bounce.
	tailReserve         int
	tailReserveBaseRows int
	// lastTokenAt is the wall-clock time the most recent assistant text token
	// arrived. IsBetweenPhases uses it to detect a streaming text entry that has
	// gone quiet — the model finished a prose segment and is now off doing work
	// (parsing the next tool_use, or stalled mid-turn) with no tokens flowing.
	// Zero means no token has arrived this turn.
	lastTokenAt       time.Time
	selection         textSelection
	scrollbarDragging bool

	// dragMouse is the last pointer position during a text-selection drag,
	// stored in LOCAL coordinates (host subtracts scrollbarTop before forwarding).
	// dragScrolling is true while the edge auto-scroll tick loop is running.
	dragMouse     tea.Mouse
	dragScrolling bool
}

// dragScrollTickMsg drives continuous auto-scroll while a selection drag is
// held past the viewport's top/bottom edge (no mouse motion required).
type dragScrollTickMsg struct{}

func dragScrollTick() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return dragScrollTickMsg{} })
}

// atScrollEdge reports whether the last drag pointer position (local coords)
// sits past the top or bottom edge of the viewport.
func (c *chatView) atScrollEdge() bool {
	row := c.dragMouse.Y // dragMouse is already in local coords
	return row < 0 || row >= c.Height()
}

// newChatView constructs a chatView sized to vpWidth × vpHeight. The host
// reserves two columns to the right of this width for the gap and scrollbar.
// root and home are resolved once at construction; used to humanize tool-call
// path arguments (relative to the project root, ~-abbreviated under home).
func newChatView(styles theme.Styles, palette theme.Palette, root, home string, vpWidth, vpHeight int) chatView {
	return chatView{
		styles:         styles,
		palette:        palette,
		root:           root,
		home:           home,
		md:             render.NewMarkdown(theme.MarkdownStyle(palette)),
		vp:             viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight)),
		vpH:            vpHeight,
		focusedToolIdx: -1,
		groupExpanded:  map[int]bool{},
		entryCache:     map[*Entry]entryRenderCache{},
		groupCache:     map[int]groupRenderCache{},
	}
}

// SetStyles swaps the chat's styles/palette and rebuilds. The markdown renderer
// is replaced wholesale so its per-width glamour cache is flushed and committed
// entries re-render in the new theme.
func (c *chatView) SetStyles(s theme.Styles, p theme.Palette) {
	c.styles = s
	c.palette = p
	c.md = render.NewMarkdown(theme.MarkdownStyle(p))
	c.stylesGen++ // invalidate every cached render built under the old theme
	c.rebuild()
}

// ── entry ownership ────────────────────────────────────────────────────────

// Entries returns the slice of scrollback entries (read-only; callers must not
// mutate the slice directly — use the methods below).
func (c *chatView) Entries() []*Entry { return c.entries }

// AppendEntry appends a single entry to the scrollback in chronological order
// (at the end). This is the right primitive for in-band turn events, which must
// preserve their arrival order relative to streamed text — e.g. a progress note
// that lands after some tokens have streamed belongs BELOW that text, and a
// tool row belongs after the text that preceded the tool call.
//
// It is deliberately NOT stream-aware. An OUT-OF-BAND notice (title rename,
// prompt-color change, mode flip) that can land mid-stream must instead go
// through AppendNotice, which hoists it ABOVE the open stream so it cannot split
// the streaming message. The distinction is intentional and cannot be inferred
// from Role alone: both an out-of-band rename and an in-band progress note
// arrive as RoleSystem during a stream but want opposite placement, so the
// choice of placement lives at the call site, expressed by which method is
// called. See AppendNotice.
func (c *chatView) AppendEntry(e *Entry) {
	c.entries = append(c.entries, e)
	// Appends do not invalidate an existing frozen prefix, but they change the
	// transcript shape once the appended entry becomes eligible for prefixing.
	c.contentGen++
}

// RemoveEntry removes one exact entry pointer from the scrollback.
func (c *chatView) RemoveEntry(target *Entry) bool {
	for i, e := range c.entries {
		if e != target {
			continue
		}
		copy(c.entries[i:], c.entries[i+1:])
		c.entries[len(c.entries)-1] = nil
		c.entries = c.entries[:len(c.entries)-1]
		c.markTranscriptDirty()
		return true
	}
	return false
}

// SetEntriesSlice replaces the entire entry slice (for /clear and applyResume).
func (c *chatView) SetEntriesSlice(es []*Entry) {
	c.entries = es
	c.flushRenderCaches()
}

// PrependBanner inserts the wordmark banner as entry zero, so it persists at
// the top of the transcript once the splash chrome is dismissed. Idempotent.
// Must be called before any tool-group interaction: groupExpanded keys are
// entry indexes, and prepending shifts them — both call sites (first submit,
// applyResume) run before the user can have expanded anything.
//
// epoch is the shimmer animation start time; passing the splash AnimModel's
// Started() keeps the sweep phase-locked across the chrome→scrollback handoff.
func (c *chatView) PrependBanner(meta banner.Meta, epoch time.Time) {
	c.bannerEpoch = epoch
	if len(c.entries) > 0 && c.entries[0].Banner != nil {
		return
	}
	c.entries = append([]*Entry{{Banner: &meta}}, c.entries...)
	c.markTranscriptDirty()
}

// bannerRows is the rendered height of the wide banner block in content lines.
const bannerRows = 8

// HasBanner reports whether entry zero is the wordmark banner.
func (c *chatView) HasBanner() bool {
	return len(c.entries) > 0 && c.entries[0].Banner != nil
}

// BannerAnimVisible reports whether any of the banner's animated rows sit
// inside the viewport's visible window. The banner is entry zero, so its rows
// are content lines [0, bannerRows); any of them shows iff YOffset < bannerRows.
// The narrow one-line fallback never animates, so a width that fails the wide
// branch counts as not visible.
func (c *chatView) BannerAnimVisible() bool {
	if !c.HasBanner() {
		return false
	}
	wrapW := c.vp.Width()
	if wrapW < 10 {
		wrapW = 10
	}
	if wrapW-entryIndent < banner.Width {
		return false
	}
	return c.vp.YOffset() < bannerRows
}

// insertNoticeAboveLast inserts e at position len-1, pushing the last entry
// down. Used by AppendNotice to keep an out-of-band notice above an open stream,
// and by the TypeDone arm to slot the ⚠ notice above the final reply. No-op
// (plain append) if entries is empty.
func (c *chatView) insertNoticeAboveLast(e *Entry) {
	n := len(c.entries)
	if n == 0 {
		c.entries = append(c.entries, e)
		c.markTranscriptDirty()
		return
	}
	c.entries = append(c.entries, nil)
	copy(c.entries[n-1+1:], c.entries[n-1:])
	c.entries[n-1] = e
	c.markTranscriptDirty()
}

// AppendNotice adds an OUT-OF-BAND system notice (title rename, prompt-color
// change, mode flip, context-regen progress, etc.) without corrupting an
// in-progress stream. Such notices are not part of the current turn's timeline;
// they are asynchronous side effects that happen to land while the model is
// streaming. A plain append would slot the notice AFTER the open (last)
// streaming entry, so the next streamed token would find a non-streaming last
// entry and open a FRESH assistant entry below the notice — splitting the
// message and, e.g., tearing a fenced code block in half (the LUNIE bug).
// Inserting the notice ABOVE the open stream keeps the streaming entry last, so
// continuation tokens keep flowing into it. When no stream is open this is an
// ordinary append.
//
// Use this for every asynchronous notice that can fire mid-stream. Use
// AppendEntry for in-band turn events (progress notes, tool rows) that must
// stay chronological relative to the streamed text.
func (c *chatView) AppendNotice(e *Entry) {
	if c.streamingTextEntry() != nil {
		c.insertNoticeAboveLast(e)
		return
	}
	c.AppendEntry(e)
}

// dropLastEntry removes the last entry. No-op if entries is empty.
func (c *chatView) dropLastEntry() {
	if n := len(c.entries); n > 0 {
		delete(c.entryCache, c.entries[n-1])
		c.entries = c.entries[:n-1]
		c.markTranscriptDirty()
	}
}

// toolEntryIndices returns the positions of every tool-call entry, in order.
// Used by the up/down nav handlers to cycle focus among tool entries.
func (c *chatView) toolEntryIndices() []int {
	var out []int
	for i, e := range c.entries {
		if e.Tool != nil {
			out = append(out, i)
		}
	}
	return out
}

// hasInProgressTool reports whether any tool entry is currently in flight
// (status == InProgress). Used by the animation tick loop to decide whether
// to keep firing — without this, the spinner on the active tool line would
// freeze once the assistant text starts streaming (the placeholder loop's
// stop condition).
// resolveStaleInProgressTools flips any tool rows still marked in-progress to a
// terminal (errored) state. Called at turn termination: a tool left in-progress
// after the turn ends never received its completion event, and while it stays
// in-progress hasInProgressTool keeps the animation tick alive indefinitely.
func (c *chatView) resolveStaleInProgressTools() {
	changed := false
	for _, e := range c.entries {
		if e.Tool != nil && e.Tool.Status == ToolStatusInProgress {
			e.Tool.Status = ToolStatusError
			if e.Tool.ResultSummary == "" {
				e.Tool.ResultSummary = "interrupted"
			}
			changed = true
		}
	}
	if changed {
		c.markTranscriptDirty()
	}
}

func (c *chatView) hasInProgressTool() bool {
	for _, e := range c.entries {
		if e.Tool != nil && e.Tool.Status == ToolStatusInProgress {
			return true
		}
	}
	return false
}

// hasLoadingTool reports whether any tool entry has a lazy body fetch in
// flight. The host's animation tick loop keeps firing while this is true so
// the expanded entry's loading spinner animates.
func (c *chatView) hasLoadingTool() bool {
	for _, e := range c.entries {
		if e.Tool != nil && e.Tool.Loading {
			return true
		}
	}
	return false
}

// TakePendingToolFetches returns and clears the tool_use_ids queued for a lazy
// body fetch by an expand toggle. The host dispatches a GetToolCall per id.
func (c *chatView) TakePendingToolFetches() []string {
	if len(c.pendingToolFetches) == 0 {
		return nil
	}
	out := c.pendingToolFetches
	c.pendingToolFetches = nil
	return out
}

// findToolEntry returns the ToolEntry whose ToolUseID matches id, or nil.
// Used by stream-event handlers to update an in-flight tool-call line.
func (c *chatView) findToolEntry(id string) *ToolEntry {
	if id == "" {
		return nil
	}
	for i := len(c.entries) - 1; i >= 0; i-- {
		if t := c.entries[i].Tool; t != nil && t.ToolUseID == id {
			return t
		}
	}
	return nil
}

// lastAssistantEntry returns the last entry with RoleAssistant, or nil.
func (c *chatView) lastAssistantEntry() *Entry {
	for i := len(c.entries) - 1; i >= 0; i-- {
		if c.entries[i].Role == RoleAssistant {
			return c.entries[i]
		}
	}
	return nil
}

// streamingTextEntry returns the currently-open assistant text entry: the last
// entry, and only if it is a streaming assistant. Returns nil when the last
// entry is anything else (tool call, system message, etc.), which signals that
// the next text starts a fresh entry positioned BELOW the tools.
func (c *chatView) streamingTextEntry() *Entry {
	if n := len(c.entries); n > 0 {
		if e := c.entries[n-1]; e.Role == RoleAssistant && e.Streaming {
			return e
		}
	}
	return nil
}

func (c *chatView) foldPendingCensor() {
	if c.pendingCensor == nil {
		return
	}
	c.pendingCensor.Superseded = true
	c.pendingCensor.SupersededOpen = false
	c.pendingCensor = nil
	c.markTranscriptDirty()
}

// FillOpenAssistant fills the open streaming placeholder with text and clears
// its Streaming flag (mirrors the chatDoneMsg fill semantics). Returns true if
// a placeholder was found and closed; false if there was no open entry (caller
// should fall back to appending). Used by the /c confirm path so the rationale
// replaces the working… placeholder rather than appending after it.
func (c *chatView) FillOpenAssistant(text string) bool {
	e := c.streamingTextEntry()
	if e == nil {
		return false
	}
	if text != "" {
		e.Content = text
	}
	e.Streaming = false
	return true
}

// rebuild re-renders the viewport content from c.entries (the authoritative
// slice). refreshViewport calls this after pushing telemetry state. It is
// equivalent to the old SetEntries(m.entries) call, but reads from the owned
// slice instead of accepting an external snapshot.
func (c *chatView) rebuild() {
	c.SetEntries(c.entries)
}

// ── transcript state machine (Apply) ─────────────────────────────────────────

// Apply runs the main-chat transcript state machine for one agent-agnostic
// transcript event, mutating c.entries. Telemetry and permission events are
// filtered by the host before Apply is called (telemetry → footer, permission
// → confirm gate). Apply never touches host footer state and never calls
// rebuild(); the host calls refreshViewport after routing, as before.
//
// Returns a tea.Cmd (always nil; present for driver symmetry).
func (c *chatView) Apply(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case chatAssistantDeltaMsg:
		// Append to the open text entry, or start a fresh one if the previous
		// segment was closed by a tool call — so post-tool prose lands BELOW the
		// tools in scrollback rather than in the pre-tool placeholder.
		e := c.streamingTextEntry()
		if e == nil {
			e = &Entry{Role: RoleAssistant, Streaming: true}
			c.AppendEntry(e)
			// A fresh reply after a watchdog challenge is the rewrite. Fold the
			// reply that triggered the challenge unless a justify call cleared it.
			c.foldPendingCensor()
		}
		e.Content += m.token
		// Stamp token arrival so IsBetweenPhases can tell a live stream from one
		// that has gone quiet mid-turn.
		c.lastTokenAt = time.Now()
		// Once real tokens arrive, the pre-stream progress note is no longer
		// relevant; clear so the renderer drops it.
		e.Status = ""

	case chatProgressMsg:
		// Warnings/notices (notably cloud retry/failover narration) must be
		// durable transcript entries. If they live only in the streaming status
		// field, the first token clears them and users miss important routing
		// changes.
		if strings.HasPrefix(m.note, "⚠ ") {
			warning := &Entry{Role: RoleSystem, Content: m.note}
			if n := len(c.entries); n > 0 && c.entries[n-1].Role == RoleAssistant && c.entries[n-1].Streaming {
				c.entries = append(c.entries[:n-1], append([]*Entry{warning}, c.entries[n-1:]...)...)
				c.markTranscriptDirty()
			} else {
				c.AppendEntry(warning)
			}
			break
		}
		// Collapse ordinary progress messages onto the open (empty) assistant
		// entry's Status field — one line that mutates as the agent advances
		// through phases. Falls back to a normal system entry if there's no open
		// streaming assistant to attach to.
		if e := c.streamingTextEntry(); e != nil && e.Content == "" {
			e.Status = m.note
		} else {
			c.AppendEntry(&Entry{Role: RoleSystem, Content: m.note})
		}

	case toolEntryStartMsg:
		// Model just emitted a tool_use block. Close the open assistant text
		// entry first: drop it if it's only the empty "thinking" placeholder or
		// a raw OpenAI-compatible tool-call JSON blob that some local runtimes
		// stream as text before their parsed tool event. Otherwise stop its
		// streaming indicator. Then drop a folded in-progress line so the user
		// sees what's being invoked. Args summary fills in on toolEntryStopMsg;
		// result fills in on toolEntryExecCompleteMsg.
		if e := c.streamingTextEntry(); e != nil {
			if e.Content == "" || looksLikeRawToolCallText(e.Content, m.name) {
				c.dropLastEntry()
			} else {
				e.Streaming = false
			}
		}
		c.AppendEntry(&Entry{
			Role: RoleSystem,
			Tool: &ToolEntry{
				ToolUseID: m.id,
				ToolName:  m.name,
				Status:    ToolStatusInProgress,
				StartedAt: time.Now(), // fallback timing anchor until exec-start tightens it
				Folded:    true,
			},
		})

	case toolEntryStopMsg:
		// Args block finished streaming — humanize the raw call JSON into a
		// readable one-liner. Silent skip if the start event was missed.
		if t := c.findToolEntry(m.id); t != nil {
			t.ArgsSummary = humanizeArgs(t.ToolName, m.argsSummary, c.root, c.home)
		}

	case toolEntryExecStartMsg:
		// Server is now running the tool. Re-anchor the timing clock here so the
		// measured duration covers execution, not arg streaming.
		if t := c.findToolEntry(m.id); t != nil {
			t.Status = ToolStatusInProgress
			t.StartedAt = time.Now()
		}

	case toolEntryExecCompleteMsg:
		// Tool finished — flip status to ✓ or ⚠ and build the result blurb
		// (detail · CLI-measured timing).
		if t := c.findToolEntry(m.id); t != nil {
			if m.isError {
				t.Status = ToolStatusError
			} else {
				t.Status = ToolStatusComplete
			}
			dur := time.Since(t.StartedAt)
			t.Duration = dur
			t.ResultSummary = humanizeResult(m.detail, m.summary, m.isError, dur)
			t.StartLine = m.startLine
			if t.ToolName == "justify" && !m.isError {
				// The model overruled the watchdog — the challenged reply
				// stands; don't fold it.
				c.pendingCensor = nil
			}
		}

	case chatAssistantMsg:
		// Whole-message append (the /c confirm rationale and any non-streaming
		// driver use this instead of delta-extend). A complete assistant entry,
		// not streaming.
		c.foldPendingCensor()
		c.AppendEntry(&Entry{Role: RoleAssistant, Content: m.text})

	case chatDoneMsg:
		e := c.streamingTextEntry()
		if e == nil && m.text != "" {
			// Tools ran but no post-tool tokens streamed; surface the final
			// answer as a fresh entry below them.
			e = &Entry{Role: RoleAssistant}
			c.AppendEntry(e)
			c.foldPendingCensor()
		}
		if e != nil {
			// If we never received any tokens, fall back to the full final response.
			if e.Content == "" {
				e.Content = m.text
			}
			e.Streaming = false
		}
		// Surface non-fatal notices (e.g. "cloud not configured — answered
		// locally") as a system entry above the assistant content.
		if m.notice != "" {
			c.insertNoticeAboveLast(&Entry{Role: RoleSystem, Content: "⚠ " + m.notice})
		}

	case chatErrorMsg:
		if e := c.streamingTextEntry(); e != nil {
			e.Streaming = false
		}
		c.AppendEntry(&Entry{Role: RoleSystem, Content: "stream error: " + m.err.Error()})

	case watchdogEventMsg:
		if m.kind == "challenge" || m.kind == "block" {
			if e := c.lastAssistantEntry(); e != nil {
				e.Streaming = false
				c.pendingCensor = e
			}
		}
		if m.kind == "echo" {
			// Dim single-line: "<thread>: <summary>"
			c.AppendEntry(&Entry{Role: RoleWatchdog, Content: m.thread + ": " + m.summary})
		} else {
			// challenge or block: header line + body line.
			header := "⚡ watchdog · " + m.protocol
			if m.kind == "block" {
				header += " (blocked — no override)"
			}
			c.AppendEntry(&Entry{Role: RoleWatchdog, Content: header})
			c.AppendEntry(&Entry{Role: RoleWatchdog, Content: m.summary})
		}
	}
	return nil
}

// ── message queue (F4a) ──────────────────────────────────────────────────────

// queuedTurn is one turn queued while a prior turn streams. It carries both
// the prompt text and any inline images so they survive the queue round-trip.
type queuedTurn struct {
	text   string
	images []agentclient.InlineImage
}

// queued holds messages submitted while a turn was streaming, FIFO. They render
// just above the prompt, drain (front) as each stream completes, and the
// most-recent (back) can be popped back into the prompt with ↑.
//
// Queued returns the display strings for each queued turn (read-only snapshot
// for the renderer). Image counts are appended when images are attached.
func (c *chatView) Queued() []string {
	out := make([]string, len(c.queued))
	for i, t := range c.queued {
		if len(t.images) > 0 {
			out[i] = fmt.Sprintf("%s  (%d image%s)", t.text, len(t.images), plural(len(t.images)))
		} else {
			out[i] = t.text
		}
	}
	return out
}

// Enqueue appends a turn to the back of the queue.
func (c *chatView) Enqueue(text string, images []agentclient.InlineImage) {
	c.queued = append(c.queued, queuedTurn{text: text, images: images})
}

// DrainNext pops the oldest queued turn off the front. Returns (zero, false)
// when the queue is empty.
func (c *chatView) DrainNext() (queuedTurn, bool) {
	if len(c.queued) == 0 {
		return queuedTurn{}, false
	}
	next := c.queued[0]
	c.queued = c.queued[1:]
	return next, true
}

// UnstageLast pops the most-recently-queued turn off the back (for the host
// to put back into the prompt for editing). Returns (zero, false) when empty.
func (c *chatView) UnstageLast() (queuedTurn, bool) {
	n := len(c.queued)
	if n == 0 {
		return queuedTurn{}, false
	}
	last := c.queued[n-1]
	c.queued = c.queued[:n-1]
	return last, true
}

// ClearQueue drops all pending turns (cancel/esc).
func (c *chatView) ClearQueue() { c.queued = nil }

// SetSize resizes the underlying viewport. Call from relayout.
func (c *chatView) SetSize(w, h int) {
	// Capture distance-from-bottom relative to OLD height before the dimensions
	// change. SetEntries consumes the pending anchor and restores the visual
	// position after reflow, so the line at the viewport's bottom stays there.
	oldTotal := c.TotalLineCount()
	oldOffset := c.YOffset()
	// Use c.vpH (manually tracked) rather than c.vp.Height(): in
	// charm.land/bubbles/v2 the Height() getter returns 0 until SetHeight() is
	// called, so the constructor's WithHeight option doesn't seed it.
	d := oldTotal - oldOffset - c.vpH
	if d < 0 {
		d = 0
	}
	c.resizeAnchor = d
	c.hasResizeAnchor = true
	c.vp.SetWidth(w)
	c.vp.SetHeight(h)
	c.vpH = h
}

// ── tool-entry navigation ──────────────────────────────────────────────────

// InToolNav reports whether the user is in tool-entry navigation mode (a tool
// entry holds keyboard focus rather than the input box).
func (c *chatView) InToolNav() bool { return c.focusedToolIdx >= 0 }

// EnterToolNav enters tool-entry navigation mode by focusing the most-recent
// tool entry. Returns true if there are tool entries to navigate; false (and
// no state change) when scrollback has no tool entries.
func (c *chatView) EnterToolNav() bool {
	indices := c.toolEntryIndices()
	if len(indices) == 0 {
		return false
	}
	c.focusedToolIdx = indices[len(indices)-1]
	return true
}

// ExitToolNav exits tool-entry navigation mode, returning focus to the input
// box. Safe to call when not in nav mode.
func (c *chatView) ExitToolNav() {
	c.focusedToolIdx = -1
}

// NavPrev moves focus to the previous (earlier) tool entry, clamped at the
// first tool entry. No-op when not in nav mode or already at the top.
func (c *chatView) NavPrev() {
	indices := c.toolEntryIndices()
	for i, idx := range indices {
		if idx == c.focusedToolIdx {
			if i > 0 {
				c.focusedToolIdx = indices[i-1]
			}
			break
		}
	}
}

// NavNext moves focus to the next (later) tool entry, clamped at the last
// tool entry. No-op when not in nav mode or already at the bottom.
func (c *chatView) NavNext() {
	indices := c.toolEntryIndices()
	for i, idx := range indices {
		if idx == c.focusedToolIdx {
			if i < len(indices)-1 {
				c.focusedToolIdx = indices[i+1]
			}
			break
		}
	}
}

// ToggleFocusedFold context-aware toggle for the focused tool entry:
//   - in a collapsed multi-entry group → expand the group (each entry becomes
//     its own per-call line)
//   - in an expanded group or a single-entry "group" → toggle the focused
//     entry's Folded (per-call line ⇄ full args+result body)
//
// No-op when not in nav mode or the focused entry has no tool data.
func (c *chatView) ToggleFocusedFold() {
	if c.focusedToolIdx < 0 || c.focusedToolIdx >= len(c.entries) {
		return
	}
	t := c.entries[c.focusedToolIdx].Tool
	if t == nil {
		return
	}
	start, end := c.focusedGroupRange()
	if start < 0 {
		return
	}
	isMulti := end > start
	// Collapsed multi-entry run → expand it.
	if isMulti && !c.groupExpanded[start] {
		c.groupExpanded[start] = true
		c.markTranscriptDirty()
		return
	}
	// Expanded run, focus on the first call and it's already folded → collapse
	// the whole run (keyboard equivalent of clicking the summary header). Any
	// other position toggles the focused call's own body.
	if isMulti && c.groupExpanded[start] && c.focusedToolIdx == start && t.Folded {
		c.groupExpanded[start] = false
		c.markTranscriptDirty()
		return
	}
	// Toggle the focused call's own body (and queue its lazy fetch on expand).
	c.toggleEntryFold(c.focusedToolIdx)
}

// focusedGroupRange returns the [start, end] inclusive entries-index range of
// the contiguous tool run containing the focused tool entry. Returns (-1,-1)
// when not in tool-nav mode or the focused entry is not a tool entry.
func (c *chatView) focusedGroupRange() (int, int) {
	if c.focusedToolIdx < 0 || c.focusedToolIdx >= len(c.entries) {
		return -1, -1
	}
	if c.entries[c.focusedToolIdx].Tool == nil {
		return -1, -1
	}
	start := c.focusedToolIdx
	for start > 0 && c.entries[start-1].Tool != nil {
		start--
	}
	end := c.focusedToolIdx
	for end+1 < len(c.entries) && c.entries[end+1].Tool != nil {
		end++
	}
	return start, end
}

// SetTurnStatus updates the live turn telemetry used while a streaming turn is
// in progress.
func (c *chatView) SetTurnStatus(ts turnStatus) {
	c.turn = ts
}

func (c *chatView) animationTime() time.Time {
	if !c.animTime.IsZero() {
		return c.animTime
	}
	return time.Now()
}

func (c *chatView) SetAnimationTime(t time.Time) {
	c.animTime = t
}

// SetStreaming mirrors the host's m.streaming so the chat can render a
// trailing "still working" indicator while waiting between phases of a
// multi-step turn. The model toggles this true on Submit and false on
// chatDoneMsg / stream cancel.
func (c *chatView) SetStreaming(s bool) {
	if s && !c.streaming && c.turn.start.IsZero() {
		c.turn.start = time.Now()
	}
	if s && !c.streaming {
		// New turn: clear any leftover token timestamp so a stale value from the
		// previous turn can't make this turn's opening (still tokenless) stream
		// read as quiescent before its first token lands.
		c.lastTokenAt = time.Time{}
		c.tailReserve = 0
		c.tailReserveBaseRows = 0
	}
	if !s {
		c.tailReserve = 0
		c.tailReserveBaseRows = 0
	}
	c.streaming = s
}

// SetTurnActivity sets the verb shown on the trailing "working" line (e.g.
// "planning sources…", "working"). Child tabs use this so their animated
// status reflects the current phase. It also anchors the elapsed clock the
// first time activity begins so the line doesn't show a bogus duration.
func (c *chatView) SetTurnActivity(activity string) {
	if c.turn.start.IsZero() {
		c.turn.start = time.Now()
	}
	c.turn.activity = activity
}

// IsBetweenPhases reports whether the turn is streaming but no visible
// activity (in-progress tool or streaming text entry) is currently the
// focus of the agent's work. That's the gap a trailing animated line
// covers — the model is processing tool results or queueing the next
// step, and the user needs SOMETHING to read as "still alive".
func (c *chatView) IsBetweenPhases() bool {
	if !c.streaming {
		return false
	}
	if c.hasInProgressTool() {
		return false
	}
	if e := c.streamingTextEntry(); e != nil {
		// An empty streaming entry is the pre-text placeholder — that's its own
		// loud indicator, so not "between phases".
		if e.Content == "" {
			return false
		}
		// A streaming entry with content that is still receiving tokens is a
		// live stream; the prose itself is the visible activity. Only once it
		// has gone quiet past the threshold — the model finished a segment and
		// is off doing work with nothing flowing — does the trailing line take
		// over. lastTokenAt is zero only before the first token, which the
		// empty-content case above already handles.
		if c.animationTime().Sub(c.lastTokenAt) < staleStreamThreshold {
			return false
		}
	}
	// There has to be at least one entry — fresh-prompt placeholder ALSO
	// counts as "between phases" but is already its own loud indicator,
	// covered by streamingTextEntry above.
	if len(c.entries) == 0 {
		return false
	}
	return true
}

// renderTrailingActivity produces the "still working" line shown at the
// tail of scrollback while IsBetweenPhases. Same loud amber-spinner +
// lime-sweep treatment the pre-text placeholder uses — the conceptual
// state is identical (model is thinking; no visible artifact yet).
func (c *chatView) renderTrailingActivity(textW int) string {
	activity := c.turn.activity
	if activity == "" {
		activity = "thinking"
	}
	t := c.animationTime()
	line := turnStatusLine(activity, t.Sub(c.turn.start), c.turn.tokOut, c.turn.model, c.turn.cloud)
	return animateSpinnerGlyphAtForPalette(t, c.palette) + " " + animateActivitySweepAt(line, t, c.palette)
}

// ── scroll surface ─────────────────────────────────────────────────────────

// Width returns the viewport width.
func (c *chatView) Width() int { return c.vp.Width() }

// Height returns the viewport height.
func (c *chatView) Height() int { return c.vp.Height() }

// DesiredHeight reports how many rows the chat wants — its rendered content lines
// plus the queued chrome rows the host pins above the prompt. A host (the /c split
// view) uses this to size the chat band so it grows with the transcript instead of
// eating the whole panel. The streaming placeholder, when open, is a real entry and
// is already counted in the content lines.
func (c *chatView) DesiredHeight() int {
	n := c.vp.TotalLineCount()
	n += len(c.queued)
	if n < 1 {
		n = 1
	}
	return n
}

// TotalLineCount returns the total number of content lines.
func (c *chatView) TotalLineCount() int { return c.vp.TotalLineCount() }

// YOffset returns the current scroll offset.
func (c *chatView) YOffset() int { return c.vp.YOffset() }

// SetYOffset sets the scroll offset.
func (c *chatView) SetYOffset(n int) { c.vp.SetYOffset(n) }

// AtBottom reports whether the viewport is scrolled to the last line.
func (c *chatView) AtBottom() bool { return c.vp.AtBottom() }

// GotoBottom scrolls to the last line.
func (c *chatView) GotoBottom() { c.vp.GotoBottom() }

// ScrollUp scrolls the viewport up by n lines.
func (c *chatView) ScrollUp(n int) { c.vp.ScrollUp(n) }

// ScrollDown scrolls the viewport down by n lines.
func (c *chatView) ScrollDown(n int) { c.vp.ScrollDown(n) }

// Update passes a bubbletea message to the underlying viewport and returns any
// resulting command. Callers outside chat_view.go must use this instead of
// accessing vp directly.
func (c *chatView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	return cmd
}

// PlainLines returns the ANSI-stripped content lines (for selection copy).
func (c *chatView) PlainLines() []string { return c.getPlainLines() }

// getPlainLines materializes the ANSI-stripped transcript lines on first use
// after a rebuild. Selection and copy are the only consumers, so the strip —
// a full parse of the rendered transcript — is off the rebuild hot path.
func (c *chatView) getPlainLines() []string {
	if c.plainDirty {
		c.plainLines = plainLines(c.content)
		c.plainDirty = false
	}
	return c.plainLines
}

// SetEntries rebuilds the viewport content from the provided entries and
// auto-scrolls to bottom only if the viewport was already there.
//
// Tool-call entries are grouped: a contiguous run of Tool-bearing entries
// renders as one rolling-consumption block (completed entries summarised on
// a single line; the in-progress entry standalone below). A blank line
// separates each block from neighbouring user/assistant/system entries.
func (c *chatView) SetEntries(entries []*Entry) {
	wasAtBottom := c.vp.AtBottom()

	prefixEnd := len(entries)
	// Keep the final entry out of the assembled prefix during ordinary streaming
	// repaints. The tail is where token/tool mutations happen, so serving the
	// frozen prefix lets repaint cost track the active entry instead of the whole
	// transcript. When the turn is between phases there is no visible active
	// entry, so all entries may be prefixed and only the animated status line is
	// rebuilt.
	if prefixEnd > 0 && !c.IsBetweenPhases() {
		prefixEnd--
	}
	if prefixEnd > 0 && entries[prefixEnd-1].Tool != nil {
		// Prefixes must end on a block boundary; backing out of a tool run avoids
		// splitting one cached tool group across prefix and dynamic suffix.
		for prefixEnd > 0 && entries[prefixEnd-1].Tool != nil {
			prefixEnd--
		}
	}

	prefix := c.renderTranscriptPrefix(entries, prefixEnd)
	var b strings.Builder
	b.Grow(len(prefix.content) + 4096)
	b.WriteString(prefix.content)
	c.arrowRows = append(c.arrowRows[:0], prefix.arrowRows...)
	// nl counts newlines written so far — which is also the content-line index
	// where the next write begins. Arrow rows are recorded against it as blocks
	// are emitted, so the map matches the layout by construction.
	nl := prefix.lineCount
	first := prefixEnd == 0
	for i := prefixEnd; i < len(entries); {
		if !first {
			b.WriteString("\n\n")
			nl += 2
		}
		if entries[i].Tool != nil {
			// Walk forward to the end of this contiguous tool run.
			j := i + 1
			for j < len(entries) && entries[j].Tool != nil {
				j++
			}
			block, rows := c.renderToolGroupCached(entries[i:j], i)
			for _, r := range rows {
				c.arrowRows = append(c.arrowRows, arrowRow{line: nl + r.Line, entry: i + r.Entry, group: r.Group, railMin: r.RailMin, railMax: r.RailMax})
			}
			b.WriteString(block)
			nl += strings.Count(block, "\n")
			i = j
		} else {
			seg := c.renderEntryCached(entries[i], i)
			if entries[i].Superseded {
				c.arrowRows = append(c.arrowRows, arrowRow{line: nl, entry: i})
				if entries[i].SupersededOpen {
					for ln := 1; ln <= strings.Count(seg, "\n"); ln++ {
						c.arrowRows = append(c.arrowRows, arrowRow{line: nl + ln, entry: i, railMin: 0, railMax: toolRailContentCol})
					}
				}
			}
			b.WriteString(seg)
			nl += strings.Count(seg, "\n")
			i++
		}
		first = false
	}
	// Trailing "still working" line: appears below the last entry while the
	// turn is in flight but no entry is the visible focus of work. Matches
	// the prose left-margin so it reads as another entry without being one.
	//
	// Once the line has appeared during a turn, keep its unfilled rows reserved
	// as blanks while the turn remains live. Otherwise the bottom-pinned viewport
	// shrinks when IsBetweenPhases flips false (for example when tokens resume),
	// making the transcript above it bounce. New streamed content consumes the
	// reserve row-for-row; turn completion clears it.
	naturalRows := 0
	if b.Len() > 0 {
		naturalRows = nl + 1
	}
	if !c.streaming {
		c.tailReserve = 0
		c.tailReserveBaseRows = 0
	} else if c.IsBetweenPhases() {
		wrapW := c.vp.Width()
		if wrapW < 10 {
			wrapW = 10
		}
		textW := wrapW - entryIndent
		if textW < 8 {
			textW = 8
		}
		pad := strings.Repeat(" ", entryIndent)
		block := indentBlock(pad, c.renderTrailingActivity(textW))
		rows := strings.Count(block, "\n") + 1
		if b.Len() > 0 {
			rows += 2
			b.WriteString("\n\n")
		}
		c.tailReserve = rows
		c.tailReserveBaseRows = naturalRows
		b.WriteString(block)
	} else if c.tailReserve > 0 {
		remaining := c.tailReserve - (naturalRows - c.tailReserveBaseRows)
		if remaining > 0 {
			appendBlankRows(&b, remaining)
		}
	}
	content := b.String()
	c.content = content
	c.plainDirty = true
	c.linkRows = collectLinkRows(content)
	c.vp.SetContent(content)
	if c.hasResizeAnchor {
		// Resize reflow: restore the same distance-from-bottom in the newly
		// wrapped content so the line that was at the viewport's bottom stays
		// there regardless of whether height grew or shrank.
		c.hasResizeAnchor = false
		newOffset := c.TotalLineCount() - c.Height() - c.resizeAnchor
		if newOffset < 0 {
			newOffset = 0
		}
		c.vp.SetYOffset(newOffset)
	} else if wasAtBottom {
		c.vp.GotoBottom()
	}
}

// appendBlankRows extends b by exactly rows visible blank rows. If b already
// has content, each added newline creates one additional blank row after the
// existing final line. If b is empty, N blank rows are represented by N-1
// newline separators because even an empty string occupies one line.
func appendBlankRows(b *strings.Builder, rows int) {
	if rows <= 0 {
		return
	}
	if b.Len() == 0 {
		if rows > 1 {
			b.WriteString(strings.Repeat("\n", rows-1))
		}
		return
	}
	b.WriteString(strings.Repeat("\n", rows))
}

// renderToolGroupBlock turns a contiguous slice of Tool-bearing entries into
// the indented group block used by SetEntries. Shares the left-margin
// indentation with renderEntry so tool blocks line up with prose.
//
// startIdx is the index of run[0] in the parent entries slice; used to look up
// per-group state (groupExpanded) and to translate the chatView-global focus
// index into a slice-local one for the renderer.
func (c *chatView) renderToolGroupBlock(run []*Entry, startIdx int) (string, []toolArrowRow) {
	wrapW := c.vp.Width()
	if wrapW < 10 {
		wrapW = 10
	}
	textW := wrapW - entryIndent
	if textW < 8 {
		textW = 8
	}
	pad := strings.Repeat(" ", entryIndent)
	tools := make([]ToolEntry, 0, len(run))
	for _, e := range run {
		tools = append(tools, *e.Tool)
	}
	opts := groupRenderOpts{
		Expanded:   c.groupExpanded[startIdx],
		FocusedIdx: -1,
	}
	if c.focusedToolIdx >= startIdx && c.focusedToolIdx < startIdx+len(run) {
		opts.Focused = true
		opts.FocusedIdx = c.focusedToolIdx - startIdx
	}
	block, rows := renderToolGroupSpans(tools, textW, c.styles, c.md, opts)
	return indentBlock(pad, block), rows
}

// View renders the viewport with a one-column scrollbar, applying selection
// highlighting internally.
func (c *chatView) View() string {
	body := c.vp.View()
	lines := strings.Split(body, "\n")
	height := c.vp.Height()
	col := scrollbarColumn(c.vp.TotalLineCount(), height, c.vp.YOffset())
	var b strings.Builder
	for i, line := range lines {
		contentLine := c.vp.YOffset() + i
		line = c.renderSelectionOnLine(line, contentLine)
		// Clamp to the viewport width so an over-wide content line (Glamour
		// pads prose a few columns past the wrap width) can't push the
		// composited row past m.width and wrap in the terminal — which would
		// shove the scrollbar onto a wrapped row and make it vanish.
		line = ansi.Truncate(line, c.vp.Width(), "")
		b.WriteString(line)
		b.WriteString(" ") // one-column gap so content doesn't touch the scrollbar
		// Guard against any row-count mismatch between the rendered body and
		// the computed column.
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(c.styles.Border.Render("█"))
			case '░':
				b.WriteString(c.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ── entry rendering ────────────────────────────────────────────────────────

func (c *chatView) renderEntry(e *Entry, idx int) string {
	wrapW := c.vp.Width()
	if wrapW < 10 {
		wrapW = 10
	}
	textW := wrapW - entryIndent
	if textW < 8 {
		textW = 8
	}
	pad := strings.Repeat(" ", entryIndent)

	// Tool-call entries get their own renderer — folded one-liner with arrow
	// marker + status glyph. Indented to match the prose left-margin so the
	// scrollback's vertical rhythm stays consistent.
	if e.Tool != nil {
		return indentBlock(pad, renderToolEntry(*e.Tool, textW, idx == c.focusedToolIdx, c.styles, c.md))
	}

	// Banner entry: the fixed-width wordmark block when it fits, otherwise a
	// compact one-liner — the 62-col chrome wraps catastrophically below that.
	if e.Banner != nil {
		if textW >= banner.Width {
			// Rendered at the current wall-clock shimmer frame; the host's
			// banner tick loop rebuilds per frame while these rows are
			// on-screen, so the sweep keeps moving in scrollback.
			return indentBlock(pad, banner.FrameAt(c.palette, *e.Banner, c.bannerEpoch))
		}
		line := c.styles.Accent.Render("CERCANO") +
			c.styles.Muted.Render(" · "+e.Banner.Tagline+" · ") +
			c.styles.Info.Render(e.Banner.Version)
		return indentBlock(pad, lipgloss.NewStyle().Width(textW).Render(line))
	}

	switch e.Role {
	case RoleUser:
		// User entries: lime ▶ marker on a full-width navy fill so prompts stand
		// out when scrolling back. Each wrapped line is padded to the content
		// width, then the navy background spans the whole line (marker + body).
		wrapped := lipgloss.NewStyle().Width(textW).Render(e.Content)
		lines := strings.Split(wrapped, "\n")
		for i := range lines {
			if i == 0 {
				lines[i] = c.styles.BufferUserMarker.Render("▶ ") + c.styles.BufferUserText.Render(lines[i])
			} else {
				lines[i] = c.styles.BufferUserText.Render(pad + lines[i])
			}
		}
		return strings.Join(lines, "\n")

	case RoleAssistant:
		if e.Superseded {
			label := c.styles.Muted.Render("▸ ⚡ superseded — rewritten after watchdog challenge")
			if !e.SupersededOpen {
				return indentBlock(pad, label)
			}
			label = c.styles.Muted.Render("▾ ⚡ superseded — rewritten after watchdog challenge")
			rendered := c.renderAssistantMarkdown(e, textW)
			if strings.TrimSpace(rendered) == "" {
				rendered = c.styles.Muted.Render("(empty reply)")
			}
			lines := strings.Split(label+"\n"+rendered, "\n")
			railBody(lines, c.styles)
			return indentBlock(pad, strings.Join(lines, "\n"))
		}

		// Pre-text placeholder: no prose yet — show the live turn status inline
		// (activity · elapsed · tokens · engine) where the agent is working.
		if e.Streaming && e.Content == "" {
			activity := c.turn.activity
			if activity == "" {
				activity = "thinking"
			}
			t := c.animationTime()
			line := turnStatusLine(activity, t.Sub(c.turn.start), c.turn.tokOut, c.turn.model, c.turn.cloud)
			content := animateSpinnerGlyphAtForPalette(t, c.palette) + " " + animateActivitySweepAt(line, t, c.palette)
			return indentBlock(pad, content)
		}
		rendered := c.renderAssistantMarkdown(e, textW)
		if e.Streaming {
			rendered += c.styles.Accent.Render(" ⟳")
		}
		return indentBlock(pad, rendered)

	case RoleSystem:
		styled := c.styles.Muted.Render(e.Content)
		wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
		return indentBlock(pad, wrapped)

	case RoleDivider:
		return indentBlock(pad, renderDivider(e.Content, textW, c.styles))

	case RoleWatchdog:
		// challenge/block header (starts with ⚡): render with Warn style so it
		// stands out. echo lines and body text render with Muted (dim).
		var styled string
		if strings.HasPrefix(e.Content, "⚡") {
			styled = c.styles.Warn.Render(e.Content)
		} else {
			styled = c.styles.Muted.Render(e.Content)
		}
		wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
		return indentBlock(pad, wrapped)
	}
	return e.Content
}

// renderDivider produces a full-width horizontal rule with a centered label,
// used to mark the freeze boundary on resume: the message communicates that
// scrollback above this line is part of the recap (the model sees a summary,
// not the verbatim turns). Label is padded with `─` on both sides to fill
// the available text width.
func renderDivider(label string, width int, styles theme.Styles) string {
	if width < 8 {
		width = 8
	}
	rule := styles.BorderDim
	// Leave 1 space gap between rule and label on each side: "─── label ───"
	labelW := lipgloss.Width(label) + 2 // +2 for the surrounding spaces
	if labelW >= width-2 {
		// Label wider than budget; just render label muted with no rule.
		return styles.Muted.Render(label)
	}
	side := (width - labelW) / 2
	left := strings.Repeat("─", side)
	right := strings.Repeat("─", width-side-labelW)
	return rule.Render(left) + " " + styles.Muted.Render(label) + " " + rule.Render(right)
}

// renderAssistantMarkdown splits the assistant buffer into completed blocks plus
// a live tail, rendering prose via Glamour and tables via the responsive Table
// renderer. Committed blocks are cached; the tail renders live (with any open
// code fence synthetically closed) so streaming code highlights as it grows.
func (c *chatView) renderAssistantMarkdown(e *Entry, textW int) string {
	content := e.Content
	if !e.Streaming && !strings.HasSuffix(content, "\n") {
		// SplitBlocks keeps an unterminated table at EOF in the live tail so the
		// streaming renderer does not commit a row until its newline arrives. Once
		// the assistant turn is frozen, EOF is a real terminator: a final table
		// without a trailing newline should still use the responsive grid renderer
		// rather than falling back to Glamour's plain Markdown table style.
		content += "\n"
	}
	blocks, tail := render.SplitBlocks(content)
	prefix := c.committedPrefix(e, blocks, textW)
	if strings.TrimSpace(tail) != "" {
		live := c.renderLiveMarkdownTail(tail, textW)
		if prefix == "" {
			return live
		}
		// committedPrefix adds a trailing "\n" after a heading block so the
		// heading isn't glued to whatever follows; here the tail is what
		// follows, so join with a single "\n" to land on one blank line.
		return prefix + "\n" + live
	}
	// A heading as the final committed block leaves a dangling trailing "\n"
	// (breathing room for a body that never came); trim it so the reply
	// doesn't end on a blank line.
	return strings.TrimRight(prefix, "\n")
}

func (c *chatView) renderLiveMarkdownTail(tail string, textW int) string {
	pinned := render.PinUntypedFences(tail)
	if !strings.Contains(pinned, "```") {
		return c.md.RenderLive(pinned, textW)
	}
	closed := closeOpenFence(pinned)
	if !strings.HasSuffix(closed, "\n") {
		closed += "\n"
	}
	blocks, rest := render.SplitBlocks(closed)
	parts := make([]string, 0, len(blocks)+1)
	for _, b := range blocks {
		parts = append(parts, c.renderMdBlock(b, textW))
	}
	if strings.TrimSpace(rest) != "" {
		parts = append(parts, c.md.RenderLive(rest, textW))
	}
	return strings.Join(parts, "\n")
}

func (c *chatView) renderMdBlock(b render.MdBlock, textW int) string {
	switch {
	case b.Kind == render.MdTable && b.Table != nil:
		return b.Table.RenderMarkdown(textW, c.styles, c.md)
	case b.Kind == render.MdCode:
		// Pin untyped fences to plaintext so chroma renders the body verbatim
		// instead of guessing a language (a wrong guess paints spurious error
		// tokens — see render.PinUntypedFences). The code-rule label below still
		// keys off b.Lang, which stays empty, so no "text" label is shown.
		body := trimBlankEdgeLines(c.md.Render(render.PinUntypedFences(b.Raw), textW))
		body = paintCodeBlockBackground(body, textW, c.palette)
		top := codeRule(b.Lang, textW, c.styles)
		bottom := codeRule("", textW, c.styles)
		return top + "\n" + body + "\n" + bottom
	default:
		return c.md.Render(b.Raw, textW)
	}
}

// ── selection surface ──────────────────────────────────────────────────────

// renderSelectionOnLine applies selection highlighting to one rendered line.
func (c *chatView) renderSelectionOnLine(line string, contentLine int) string {
	start, end, ok := c.selection.lineRange(contentLine, c.vp.Width())
	if !ok {
		return line
	}
	return highlightRange(line, start, end, theme.SelectionBackgroundSGR(c.palette))
}

// selectedText returns the plain-text content covered by the current selection.
func (c *chatView) selectedText() string {
	plainLns := c.getPlainLines()
	if !c.selection.hasRange() || len(plainLns) == 0 {
		return ""
	}
	start, end := c.selection.ordered()
	start.Line = clampInt(start.Line, 0, len(plainLns)-1)
	end.Line = clampInt(end.Line, 0, len(plainLns)-1)
	if beforePoint(end, start) {
		return ""
	}

	parts := make([]string, 0, end.Line-start.Line+1)
	for line := start.Line; line <= end.Line; line++ {
		text := plainLns[line]
		switch {
		case start.Line == end.Line:
			parts = append(parts, ansi.Cut(text, start.Col, end.Col))
		case line == start.Line:
			parts = append(parts, ansi.Cut(text, start.Col, ansi.StringWidth(text)))
		case line == end.Line:
			parts = append(parts, ansi.Cut(text, 0, end.Col))
		default:
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// selectionPointFromLocal converts a LOCAL coordinate (row already relative to
// the viewport top, i.e. host has subtracted scrollbarTop) to a selectionPoint.
// If allowScroll is true and the row is out of bounds, the viewport is scrolled
// one line in the appropriate direction.
func (c *chatView) selectionPointFromLocal(localX, localY int, allowScroll bool) selectionPoint {
	height := c.vp.Height()
	row := localY
	if allowScroll {
		switch {
		case row < 0:
			c.vp.ScrollUp(1)
			row = 0
		case row >= height:
			c.vp.ScrollDown(1)
			row = height - 1
		}
	}
	row = clampInt(row, 0, maxInt(0, height-1))
	line := c.vp.YOffset() + row
	if pl := c.getPlainLines(); len(pl) > 0 {
		line = clampInt(line, 0, len(pl)-1)
	}
	return selectionPoint{
		Line: line,
		Col:  clampInt(localX, 0, c.vp.Width()),
	}
}

// MouseInText reports whether a LOCAL coordinate falls inside the viewport text
// region (not on the scrollbar column).
func (c *chatView) MouseInText(localX, localY int) bool {
	return localX >= 0 &&
		localX < c.vp.Width() &&
		localY >= 0 &&
		localY < c.vp.Height()
}

// MouseToggleFold checks whether a local click landed on a tool entry's
// arrow row and, if so, focuses that entry and toggles its Folded state
// (mirroring the keyboard ToggleFocusedFold path). Returns true when the
// click was handled — the host should refresh the viewport and skip its
// selection begin. Only the arrow row itself claims a click; expanded tool
// bodies and prose fall through so text selection works everywhere else.
func collectLinkRows(content string) []linkRow {
	lines := strings.Split(content, "\n")
	rows := make([]linkRow, 0)
	for lineNo, line := range lines {
		rows = append(rows, collectOSC8LinkRows(line, lineNo)...)
		plain := ansi.Strip(line)
		for _, loc := range bareURLRe.FindAllStringIndex(plain, -1) {
			url := strings.TrimRight(plain[loc[0]:loc[1]], `.,;:!?]}`)
			if url == "" {
				continue
			}
			rows = append(rows, linkRow{line: lineNo, start: ansi.StringWidth(plain[:loc[0]]), end: ansi.StringWidth(plain[:loc[0]+len(url)]), url: url})
		}
	}
	return rows
}

func collectOSC8LinkRows(line string, lineNo int) []linkRow {
	var rows []linkRow
	active := ""
	spanStart := -1
	col := 0
	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], "\x1b]8;") {
			if active != "" && spanStart >= 0 && col > spanStart {
				rows = append(rows, linkRow{line: lineNo, start: spanStart, end: col, url: active})
			}
			payloadStart := i + len("\x1b]")
			payloadEnd, seqEnd := oscEnd(line, payloadStart)
			if seqEnd < 0 {
				break
			}
			active = osc8URL(line[payloadStart:payloadEnd])
			if active == "" {
				spanStart = -1
			} else {
				spanStart = col
			}
			i = seqEnd
			continue
		}
		if line[i] == '\x1b' {
			i = skipANSI(line, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		col += ansi.StringWidth(string(r))
		i += size
	}
	if active != "" && spanStart >= 0 && col > spanStart {
		rows = append(rows, linkRow{line: lineNo, start: spanStart, end: col, url: active})
	}
	return rows
}

func osc8URL(payload string) string {
	parts := strings.SplitN(payload, ";", 3)
	if len(parts) != 3 || parts[0] != "8" {
		return ""
	}
	return parts[2]
}

func oscEnd(s string, start int) (payloadEnd, seqEnd int) {
	for i := start; i < len(s); i++ {
		if s[i] == '\a' {
			return i, i + 1
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
			return i, i + 2
		}
	}
	return -1, -1
}

func skipANSI(s string, i int) int {
	if i+1 >= len(s) {
		return i + 1
	}
	if s[i+1] == '[' {
		j := i + 2
		for j < len(s) {
			if s[j] >= '@' && s[j] <= '~' {
				return j + 1
			}
			j++
		}
		return len(s)
	}
	if s[i+1] == ']' {
		_, end := oscEnd(s, i+2)
		if end >= 0 {
			return end
		}
		return len(s)
	}
	return i + 2
}

func (c *chatView) LinkAt(localX, localY int) (string, bool) {
	if !c.MouseInText(localX, localY) {
		return "", false
	}
	line := c.vp.YOffset() + localY
	for _, r := range c.linkRows {
		if r.line == line && localX >= r.start && localX < r.end && r.url != "" {
			return r.url, true
		}
	}
	return "", false
}

func (c *chatView) SubAgentTabAt(localX, localY int) (string, bool) {
	if !c.MouseInText(localX, localY) {
		return "", false
	}
	r, ok := c.arrowRowAt(c.vp.YOffset()+localY, localX)
	if !ok || r.group || localX <= 3 || r.entry < 0 || r.entry >= len(c.entries) {
		return "", false
	}
	tool := c.entries[r.entry].Tool
	if tool == nil || tool.SubAgentID == "" {
		return "", false
	}
	return tool.SubAgentID, true
}

func (c *chatView) attachSubAgentToTool(toolUseID, subAgentID string) {
	if toolUseID == "" || subAgentID == "" {
		return
	}
	for _, e := range c.entries {
		if e == nil || e.Tool == nil || e.Tool.ToolUseID != toolUseID {
			continue
		}
		e.Tool.SubAgentID = subAgentID
		c.markTranscriptDirty()
		return
	}
}

func (c *chatView) MouseToggleFold(localX, localY int) bool {
	if !c.MouseInText(localX, localY) {
		return false
	}
	r, ok := c.arrowRowAt(c.vp.YOffset()+localY, localX)
	if !ok {
		return false
	}
	// A mouse interaction is not keyboard navigation: clear any focus caret so
	// it never renders on a mouse-driven expand, and a stale caret from an
	// earlier keyboard nav doesn't linger. The ▶ caret is a keyboard-only
	// affordance.
	c.focusedToolIdx = -1
	if r.entry >= 0 && r.entry < len(c.entries) && c.entries[r.entry].Superseded {
		c.entries[r.entry].SupersededOpen = !c.entries[r.entry].SupersededOpen
		c.markTranscriptDirty()
		return true
	}
	if r.group {
		// The summary header, or the outer group rail, collapses/expands the run.
		c.toggleGroup(r.entry)
	} else {
		// A per-call arrow, or that entry's own rail, toggles just that call's body.
		c.toggleEntryFold(r.entry)
	}
	return true
}

// toggleGroup flips a multi-entry tool run between its collapsed summary and
// the expanded per-call view. start is the entries index of the run's first
// tool entry — the key groupExpanded is recorded under.
func (c *chatView) toggleGroup(start int) {
	c.groupExpanded[start] = !c.groupExpanded[start]
	c.markTranscriptDirty()
}

// toggleEntryFold flips one tool entry between its folded one-liner and its
// expanded args+result body. No-op if idx is out of range or not a tool entry.
func (c *chatView) toggleEntryFold(idx int) {
	if idx < 0 || idx >= len(c.entries) {
		return
	}
	t := c.entries[idx].Tool
	if t == nil {
		return
	}
	t.Folded = !t.Folded
	c.markTranscriptDirty()
	// Expanding a call whose full body hasn't been fetched yet queues a lazy
	// GetToolCall and shows a loading spinner until it returns.
	if !t.Folded && !t.Loading && t.FullArgs == "" && t.FullResult == "" && t.ToolUseID != "" {
		t.Loading = true
		c.pendingToolFetches = append(c.pendingToolFetches, t.ToolUseID)
	}
}

// arrowRow maps an absolute content line (an index into plainLines) to a
// clickable fold toggle drawn on it. group distinguishes the two toggle
// levels: a group row (the summary header) expands/collapses the whole tool
// run and keys groupExpanded by entry (the run's first index); a non-group
// row toggles that single tool entry's own body.
type arrowRow struct {
	line    int
	entry   int
	group   bool
	railMin int
	railMax int // > 0 → a rail row claiming [railMin, railMax); 0 → full-width toggle
}

type linkRow struct {
	line       int
	start, end int
	url        string
}

var bareURLRe = regexp.MustCompile(`https?://[^\s<>()]+`)

// arrowRowAt returns the clickable row at (line, x). A bounded rail row
// (railMax > 0) claims a click only within [railMin, railMax); a full-width
// toggle row (railMax == 0) claims any x on its line. Rail rows take
// precedence, so clicking a rail's gutter collapses even where a full-width
// arrow also covers the line. Returns false when nothing claims the point (body
// text to the right of the rails is left to text selection).
func (c *chatView) arrowRowAt(line, x int) (arrowRow, bool) {
	var full arrowRow
	haveFull := false
	for _, r := range c.arrowRows {
		if r.line != line {
			continue
		}
		if r.railMax > 0 {
			if x >= r.railMin && x < r.railMax {
				return r, true
			}
		} else {
			full = r
			haveFull = true
		}
	}
	if haveFull {
		return full, true
	}
	return arrowRow{}, false
}

// ClearSelection resets the selection state.
func (c *chatView) ClearSelection() {
	c.selection = textSelection{}
}

// SelectionActive reports whether a selection is active.
func (c *chatView) SelectionActive() bool { return c.selection.Active }

// SelectionHasRange reports whether the selection covers a non-empty range.
func (c *chatView) SelectionHasRange() bool { return c.selection.hasRange() }

// SelectionDragging reports whether a selection drag is in progress.
func (c *chatView) SelectionDragging() bool { return c.selection.Dragging }

// ── scrollbar drag surface ─────────────────────────────────────────────────

// ScrollbarHit reports whether a local point is on the grabbable scrollbar
// column. The host reserves two columns to the right of the viewport: column
// Width() is the gap, Width()+1 is the bar. Accept Width()+1 and one more —
// terminals sometimes report the rightmost visible cell as Width()+2.
// This is equivalent to the old host-side test `mouse.X >= m.width-1` because
// production sets vp.Width() = m.width-2, so Width()+1 = m.width-1.
func (c *chatView) ScrollbarHit(localX, localY int) bool {
	return localX >= c.vp.Width()+1 && localY >= 0 && localY < c.vp.Height()
}

// scrollbarScrub jumps the scroll offset to match the local click row.
func (c *chatView) scrollbarScrub(localY int) {
	off := scrollOffsetFromClick(localY, 0, c.vp.Height(), c.vp.TotalLineCount())
	c.vp.SetYOffset(off)
}

// ScrollbarDragging reports whether a scrollbar drag is in progress.
func (c *chatView) ScrollbarDragging() bool { return c.scrollbarDragging }

// StopScrollbarDrag clears the scrollbar drag flag.
func (c *chatView) StopScrollbarDrag() { c.scrollbarDragging = false }

// ClearSelectionDrag clears the selection's Dragging flag without clearing the
// selection itself. Used when a competing gesture (pending confirm, etc.) must
// cancel an in-progress selection drag.
func (c *chatView) ClearSelectionDrag() { c.selection.Dragging = false }

// MouseDrag handles a drag motion event in local viewport coordinates.
// If a scrollbar drag is active it scrubs; if a selection drag is active it
// extends the selection and starts the edge auto-scroll tick when needed.
// Returns the tick cmd (or nil) so the host can return it directly.
func (c *chatView) MouseDrag(localX, localY int) tea.Cmd {
	if c.scrollbarDragging {
		c.scrollbarScrub(localY)
		return nil
	}
	if !c.selection.Dragging {
		return nil
	}
	c.dragMouse = tea.Mouse{X: localX, Y: localY}
	c.updateSelection(localX, localY, true)
	if c.atScrollEdge() && !c.dragScrolling {
		c.dragScrolling = true
		return dragScrollTick()
	}
	return nil
}

// DragScrollTick is called by the host when a dragScrollTickMsg arrives.
// It continues the edge auto-scroll loop while the drag is held at an edge.
// Returns (cmd, true) when the loop reschedules, (nil, false) when it stops.
func (c *chatView) DragScrollTick() (tea.Cmd, bool) {
	if !c.selection.Dragging || !c.atScrollEdge() {
		c.dragScrolling = false
		return nil, false
	}
	c.updateSelection(c.dragMouse.X, c.dragMouse.Y, true)
	return dragScrollTick(), true
}

// StopDragScroll clears the edge auto-scroll flag.
func (c *chatView) StopDragScroll() { c.dragScrolling = false }

// DragScrolling reports whether the edge auto-scroll tick loop is active.
func (c *chatView) DragScrolling() bool { return c.dragScrolling }

// Wheel scrolls the viewport up or down by promptWheelDelta lines.
// Provisional for step-4 reuse; the host's chat-wheel path keeps Update(msg)
// for byte-identical behavior (zero behavior change for now).
func (c *chatView) Wheel(up bool) {
	if up {
		c.ScrollUp(promptWheelDelta)
	} else {
		c.ScrollDown(promptWheelDelta)
	}
}

// selectionEmpty reports whether the selection covers no range.
func (c *chatView) selectionEmpty() bool { return c.selection.empty() }

// beginSelection starts a new selection drag anchored at localX/localY.
func (c *chatView) beginSelection(localX, localY int) {
	pt := c.selectionPointFromLocal(localX, localY, false)
	c.selection = textSelection{
		Active:   true,
		Dragging: true,
		Anchor:   pt,
		Cursor:   pt,
	}
}

// updateSelection extends the selection cursor to localX/localY (host has
// already translated from screen coords). If allowScroll is true and the row
// is out of bounds, the viewport is scrolled one line in the appropriate
// direction.
func (c *chatView) updateSelection(localX, localY int, allowScroll bool) {
	if !c.selection.Active {
		return
	}
	c.selection.Cursor = c.selectionPointFromLocal(localX, localY, allowScroll)
}

// MouseDown is the unified handler for a left-click inside the viewport region.
// localX/localY are already translated to viewport-local coordinates (host
// subtracts scrollbarTop from Y).
func (c *chatView) MouseDown(localX, localY int) {
	if c.ScrollbarHit(localX, localY) {
		// A bar grab is a scroll gesture, not a text selection; cancel any
		// in-progress selection drag so it can't hijack subsequent motion.
		c.selection.Dragging = false
		c.scrollbarDragging = true
		c.scrollbarScrub(localY)
		return
	}
	if c.MouseInText(localX, localY) {
		c.scrollbarDragging = false
		c.beginSelection(localX, localY)
		return
	}
	c.ClearSelection()
}

// MouseUp finalizes a left release in the viewport (local coords). It stops any
// edge auto-scroll and scrollbar drag. If a selection drag was in progress and
// covers a non-empty range, the text is auto-copied; copied=true signals the
// host to set its status notice. cmd is the clipboard cmd or nil.
func (c *chatView) MouseUp(localX, localY int) (tea.Cmd, bool) {
	c.StopDragScroll()
	if c.selection.Dragging {
		c.updateSelection(localX, localY, true)
		c.selection.Dragging = false
		if c.selection.empty() {
			c.ClearSelection()
		} else if text := c.selectedText(); text != "" {
			c.scrollbarDragging = false
			return selectionClipboardCmd(text), true
		}
	}
	c.scrollbarDragging = false
	return nil, false
}

// HandleSelectionKey handles a key press while a selection is active.
// Returns (cmd, handled, copied): cmd is a clipboard cmd or nil; handled=true
// means the host should not process the key further; copied=true means the host
// should set its status notice.
func (c *chatView) HandleSelectionKey(msg tea.KeyPressMsg) (tea.Cmd, bool, bool) {
	switch msg.String() {
	case "esc":
		c.ClearSelection()
		return nil, true, false
	case "enter", "c", "y", "ctrl+c":
		text := c.selectedText()
		c.ClearSelection()
		if text == "" {
			return nil, true, false
		}
		return selectionClipboardCmd(text), true, true
	}
	if isSelectionCopyKey(msg) {
		text := c.selectedText()
		c.ClearSelection()
		if text == "" {
			return nil, true, false
		}
		return selectionClipboardCmd(text), true, true
	}
	if msg.Text != "" {
		c.ClearSelection()
	}
	return nil, false, false
}
