package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type chatTab struct {
	id       string
	parentID string
	title    string
	tools    []string
	view     chatView
	done     bool
	errored  bool
	// restored marks a tab rebuilt from persisted transcripts on resume
	// (not a live dispatch). cleanupFinishedSubAgentTabs spares these so a
	// reopened sub-agent tab is not swept on the next turn merely because
	// it is already finished.
	restored bool
}

// chatTabSurface owns every chat view, including main. Under Bubble Tea's
// value-copy Update semantics the Model struct is copied every frame, so the
// canonical chat state cannot live in a plain Model field — it lives here,
// behind the shared tabs map's *chatTab pointers, which survive the copy.
// mainChat()/activeChat() are the only sanctioned ways to reach a view.
type chatTabSurface struct {
	active      string
	order       []string
	tabs        map[string]*chatTab
	childCounts map[string]int
	closed      map[string]bool // ids the user dismissed; late events are dropped
	focused     bool            // keyboard focus is on the strip, not the prompt
}

const mainChatTabID = "main"

// initChatTabs builds the surface with the main tab already resident in the
// map. Called once from New(); ensureChatTabs is the idempotent guard for any
// path that runs before New()'s copy propagates (e.g. tests constructing a
// bare Model).
func (m *Model) initChatTabs() {
	// A zero chatView here (not newChatView) so a bare Model{} — e.g. tests
	// constructed before styles/palette are set — never triggers markdown
	// styling on an empty palette. New() seeds the real styled main view via
	// setMainChat.
	m.chatTabs = chatTabSurface{
		active:      mainChatTabID,
		order:       []string{mainChatTabID},
		tabs:        map[string]*chatTab{mainChatTabID: {id: mainChatTabID, title: "main"}},
		childCounts: map[string]int{},
		closed:      map[string]bool{},
	}
}

func (m *Model) ensureChatTabs() {
	if m.chatTabs.tabs == nil {
		m.initChatTabs()
	}
	if m.chatTabs.childCounts == nil {
		m.chatTabs.childCounts = map[string]int{}
	}
	if m.chatTabs.closed == nil {
		m.chatTabs.closed = map[string]bool{}
	}
	if _, ok := m.chatTabs.tabs[mainChatTabID]; !ok {
		m.chatTabs.tabs[mainChatTabID] = &chatTab{id: mainChatTabID, title: "main"}
		m.chatTabs.order = append([]string{mainChatTabID}, m.chatTabs.order...)
	}
	if m.chatTabs.active == "" {
		m.chatTabs.active = mainChatTabID
	}
}

// mainChat returns the main agent's canonical view. All main-turn content
// (assistant text, tool rows, system lines, queued input) targets this.
func (m *Model) mainChat() *chatView {
	m.ensureChatTabs()
	return &m.chatTabs.tabs[mainChatTabID].view
}

// activeChat returns the focused tab's view. All viewport interaction (scroll,
// selection, tool-nav, mouse) and body rendering target this so input follows
// what the user is looking at.
func (m *Model) activeChat() *chatView {
	m.ensureChatTabs()
	if tab, ok := m.chatTabs.tabs[m.chatTabs.active]; ok {
		return &tab.view
	}
	return &m.chatTabs.tabs[mainChatTabID].view
}

func (m *Model) ensureSubAgentTab(id, parentID, title string, tools []string) *chatView {
	m.ensureChatTabs()
	if tab, ok := m.chatTabs.tabs[id]; ok {
		if parentID != "" && tab.parentID == "" {
			tab.parentID = parentID
		}
		if title != "" && title != "sub" {
			tab.title = title
		}
		if len(tools) > 0 {
			tab.tools = append([]string(nil), tools...)
		}
		return &tab.view
	}
	title = m.nextSubAgentTitle(parentID, title)
	vpW := m.width - 2
	if vpW < 1 {
		vpW = 1
	}
	vpH := m.mainChat().Height()
	if vpH < 1 {
		vpH = 1
	}
	view := newChatView(m.styles, m.palette, m.root, m.home, vpW, vpH)
	kind := "Sub-agent"
	if strings.HasPrefix(id, "activity:") {
		kind = "Activity"
	}
	view.AppendEntry(&Entry{Role: RoleSystem, Content: fmt.Sprintf("%s %s started", kind, title)})
	tab := &chatTab{id: id, parentID: parentID, title: title, tools: append([]string(nil), tools...), view: view}
	m.chatTabs.tabs[id] = tab
	m.chatTabs.order = append(m.chatTabs.order, id)
	// Deliberately do NOT steal focus: the parent turn is still streaming in
	// main while its dispatch runs. The new tab shows in the strip with an
	// activity dot; the user switches to it when they choose.
	return &tab.view
}

func (m *Model) nextSubAgentTitle(parentID, requested string) string {
	if requested != "" && requested != "sub" {
		return requested
	}
	counterKey := mainChatTabID
	prefix := "sub"
	if parentID != "" {
		if parent, ok := m.chatTabs.tabs[parentID]; ok && parent != nil && parent.id != mainChatTabID {
			counterKey = parentID
			prefix = parent.title
		}
	}
	m.chatTabs.childCounts[counterKey]++
	ordinal := m.chatTabs.childCounts[counterKey]
	if prefix == "sub" {
		return fmt.Sprintf("sub %d", ordinal)
	}
	return fmt.Sprintf("%s.%d", prefix, ordinal)
}

func (m *Model) chatTabItems() []tabStripItem {
	m.ensureChatTabs()
	items := make([]tabStripItem, 0, len(m.chatTabs.order))
	for _, id := range m.chatTabs.order {
		tab := m.chatTabs.tabs[id]
		if tab == nil {
			continue
		}
		label := tab.title
		if tab.errored {
			label += " !"
		} else if !tab.done && id != mainChatTabID {
			label += " •"
		}
		items = append(items, tabStripItem{ID: id, Label: label, Closable: id != mainChatTabID})
	}
	return items
}

func (m Model) hasSubAgentTabs() bool {
	return len(m.chatTabs.order) > 1
}

func (m Model) renderChatTabStrip() string {
	return renderTabStrip(m.width, m.chatTabItems(), m.chatTabs.active, m.chatTabs.focused, m.styles)
}

func (m *Model) switchChatTab(id string) bool {
	m.ensureChatTabs()
	if _, ok := m.chatTabs.tabs[id]; !ok {
		return false
	}
	m.chatTabs.active = id
	m.activeChat().rebuild()
	return true
}

// closeActiveSubAgentTab dismisses the focused sub-agent tab. Main is never
// closable. The id is remembered as closed so any later streamed events for it
// are dropped rather than resurrecting the tab. Focus falls back to the
// previous tab in the strip, ending at main.
func (m *Model) reopenSubAgentTabCmd(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	delete(m.chatTabs.closed, id)
	if m.agent == nil {
		view := m.ensureSubAgentTab(id, "", "", nil)
		view.AppendEntry(&Entry{Role: RoleSystem, Content: "reopened sub-agent tab"})
		view.rebuild()
		m.switchChatTab(id)
		return nil
	}
	agent := m.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		turns, err := agent.ResumeConversation(ctx, id)
		return subAgentReopenMsg{id: id, turns: turns, err: err}
	}
}

func (m *Model) applySubAgentReopen(msg subAgentReopenMsg) {
	if msg.id == "" {
		return
	}
	delete(m.chatTabs.closed, msg.id)
	view := m.ensureSubAgentTab(msg.id, "", "", nil)
	if msg.err != nil {
		view.AppendEntry(&Entry{Role: RoleSystem, Content: "failed to reopen sub-agent tab: " + msg.err.Error()})
	} else {
		view.SetEntriesSlice(resumeEntries(msg.turns, 0))
		if tab := m.chatTabs.tabs[msg.id]; tab != nil {
			tab.done = true
			tab.restored = true
		}
	}
	view.rebuild()
	m.switchChatTab(msg.id)
}

func (m *Model) closeActiveSubAgentTab() bool {
	m.ensureChatTabs()
	return m.closeSubAgentTab(m.chatTabs.active)
}

// closeSubAgentTab dismisses the sub-agent tab with the given id (from an [x]
// click or the close key). Main is never closable. The id is remembered as
// closed so late streamed events don't resurrect it. If the closed tab was the
// active one, focus falls back to the previous tab; strip focus is released
// once no sub tabs remain.
func (m *Model) closeSubAgentTab(id string) bool {
	m.ensureChatTabs()
	if id == mainChatTabID {
		return false
	}
	if _, ok := m.chatTabs.tabs[id]; !ok {
		return false
	}
	fallback := mainChatTabID
	order := make([]string, 0, len(m.chatTabs.order))
	for i, oid := range m.chatTabs.order {
		if oid == id {
			if i > 0 {
				fallback = m.chatTabs.order[i-1]
			}
			continue
		}
		order = append(order, oid)
	}
	m.chatTabs.order = order
	delete(m.chatTabs.tabs, id)
	m.chatTabs.closed[id] = true
	m.dismissSubAgentTab(id) // persist the close so a resume doesn't reopen it
	if m.chatTabs.active == id {
		m.chatTabs.active = fallback
	}
	if !m.hasSubAgentTabs() {
		m.chatTabs.focused = false
	}
	m.activeChat().rebuild()
	return true
}

// cleanupFinishedSubAgentTabs prunes every finished (done, including errored)
// sub-agent tab at the start of a new main turn. It deliberately spares the
// tab the user is currently viewing (m.chatTabs.active): a finished tab is
// still worth reading, so yanking it mid-review would rip the transcript out
// from under the cursor. Deferring the sweep to turn start means finished tabs
// only vanish once the user has moved on to new work. Removed ids are NOT added
// to the closed set — that set is reserved for tabs the user explicitly
// dismissed, whose late events we drop; a finished tab that later streams more
// events should be free to reappear.
// dropSubAgentTabs removes every sub-agent tab (keeping main), resetting the
// child-ordinal counters and strip focus. Called at the top of a resume's tab
// restore so a freshly-resumed conversation's sub-agent tabs number from 1 and
// never blend with tabs left over from a previously-active conversation.
func (m *Model) dropSubAgentTabs() {
	m.ensureChatTabs()
	for id := range m.chatTabs.tabs {
		if id != mainChatTabID {
			delete(m.chatTabs.tabs, id)
		}
	}
	m.chatTabs.order = []string{mainChatTabID}
	m.chatTabs.childCounts = map[string]int{}
	m.chatTabs.active = mainChatTabID
	m.chatTabs.focused = false
}

func (m *Model) cleanupFinishedSubAgentTabs() {
	m.ensureChatTabs()
	order := make([]string, 0, len(m.chatTabs.order))
	for _, id := range m.chatTabs.order {
		tab := m.chatTabs.tabs[id]
		if id == mainChatTabID || tab == nil || !tab.done || tab.restored {
			order = append(order, id)
			continue
		}
		// Keep the active tab for post-mortem only when it contains something
		// worth reading. A failed/abandoned dispatch can leave behind a tab with
		// only the synthetic "started"/"tools" lifecycle lines; preserving that
		// tab traps the user in an empty error surface and reopens it on resume.
		if id == m.chatTabs.active && (!tab.errored || subAgentTabHasSubstantiveTranscript(tab)) {
			order = append(order, id)
			continue
		}
		delete(m.chatTabs.tabs, id)
		m.dismissSubAgentTab(id) // persist the sweep so a resume doesn't reopen it
	}
	m.chatTabs.order = order
	if _, ok := m.chatTabs.tabs[m.chatTabs.active]; !ok {
		m.chatTabs.active = mainChatTabID
	}
	if !m.hasSubAgentTabs() {
		m.chatTabs.focused = false
	}
}

func subAgentTabHasSubstantiveTranscript(tab *chatTab) bool {
	if tab == nil {
		return false
	}
	for _, e := range tab.view.Entries() {
		if e == nil {
			continue
		}
		if e.Tool != nil {
			return true
		}
		content := strings.TrimSpace(e.Content)
		if e.Role == RoleAssistant && content != "" {
			return true
		}
		if e.Role == RoleUser && content != "" {
			return true
		}
		if e.Role == RoleSystem && (strings.HasPrefix(content, "sub-agent failed:") || strings.Contains(content, " failed:")) {
			return true
		}
	}
	return false
}

func (m *Model) finishStaleSubAgentTabs(reason string) {
	m.ensureChatTabs()
	for _, id := range m.chatTabs.order {
		if id == mainChatTabID {
			continue
		}
		tab := m.chatTabs.tabs[id]
		if tab == nil || tab.done || tab.restored {
			continue
		}
		tab.done = true
		tab.errored = true
		if reason != "" {
			tab.view.AppendEntry(&Entry{Role: RoleSystem, Content: reason})
			tab.view.rebuild()
		}
	}
}

func (m *Model) applySubAgentEvent(ev subAgentEventMsg) {
	if ev.id == "" {
		ev.id = ev.title
	}
	if ev.id == "" {
		ev.id = "sub"
	}
	m.ensureChatTabs()
	// A tab the user explicitly closed stays closed: drop its late events
	// instead of springing the tab back to life.
	if m.chatTabs.closed[ev.id] {
		return
	}
	view := m.ensureSubAgentTab(ev.id, ev.parentID, ev.title, ev.tools)
	if ev.kind == "started" && ev.toolUseID != "" {
		m.mainChat().attachSubAgentToTool(ev.toolUseID, ev.id)
	}
	if len(ev.tools) > 0 && ev.kind == "started" {
		view.AppendEntry(&Entry{Role: RoleSystem, Content: "Tools: " + strings.Join(ev.tools, ", ")})
	}
	if len(ev.ignored) > 0 {
		view.AppendEntry(&Entry{Role: RoleSystem, Content: "Ignored requested tools: " + strings.Join(ev.ignored, ", ")})
	}
	if ev.inner != nil {
		view.Apply(ev.inner)
	} else if ev.text != "" {
		role := RoleSystem
		if ev.kind == "prompt" {
			role = RoleUser
		}
		view.AppendEntry(&Entry{Role: role, Content: ev.text})
	}
	if tab := m.chatTabs.tabs[ev.id]; tab != nil {
		switch ev.kind {
		case "done":
			tab.done = true
		case "error":
			tab.done = true
			tab.errored = true
		}
	}
	view.rebuild()
}

// setMainChat installs v as the main tab's view, (re)initializing the tab
// surface with a single main tab. Used by New via initChatTabs and by tests
// that construct a Model directly instead of through New.
func (m *Model) setMainChat(v chatView) {
	m.chatTabs = chatTabSurface{
		active:      mainChatTabID,
		order:       []string{mainChatTabID},
		tabs:        map[string]*chatTab{mainChatTabID: {id: mainChatTabID, title: "main", view: v}},
		childCounts: map[string]int{},
		closed:      map[string]bool{},
	}
}

// cycleChatTab returns the id dir steps from the active tab, wrapping around
// the strip order.
func (m *Model) cycleChatTab(dir int) string {
	m.ensureChatTabs()
	n := len(m.chatTabs.order)
	if n == 0 {
		return mainChatTabID
	}
	idx := 0
	for i, id := range m.chatTabs.order {
		if id == m.chatTabs.active {
			idx = i
			break
		}
	}
	idx = (idx + dir + n) % n
	return m.chatTabs.order[idx]
}

// handleChatTabStripKey drives keyboard focus and navigation for the chat tab
// strip, mirroring the settings strip. shift+tab lifts focus from the prompt;
// while focused, tab/arrows cycle, digits jump, x closes, enter/esc exit. It
// returns true when it consumed the key. An unrecognized key while focused
// drops focus and returns false, so that key reaches the prompt as input.
func (m *Model) handleChatTabStripKey(keyStr string) bool {
	if !m.hasSubAgentTabs() {
		return false
	}
	if !m.chatTabs.focused {
		if keyStr == "shift+tab" {
			m.chatTabs.focused = true
			return true
		}
		return false
	}
	switch keyStr {
	case "tab", "right":
		m.switchChatTab(m.cycleChatTab(+1))
	case "shift+tab", "left":
		m.switchChatTab(m.cycleChatTab(-1))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if idx := int(keyStr[0] - '1'); idx < len(m.chatTabs.order) {
			m.switchChatTab(m.chatTabs.order[idx])
		}
	case "x":
		m.closeActiveSubAgentTab()
		if !m.hasSubAgentTabs() {
			m.chatTabs.focused = false
		}
	case "enter", "esc":
		m.chatTabs.focused = false
	default:
		// Unknown key: leave focus and let the prompt handle it.
		m.chatTabs.focused = false
		return false
	}
	return true
}
