package ui

import (
	"fmt"
	"strings"
)

type chatTab struct {
	id       string
	parentID string
	title    string
	tools    []string
	view     chatView
	done     bool
	errored  bool
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
	view.AppendEntry(&Entry{Role: RoleSystem, Content: fmt.Sprintf("Sub-agent %s started", title)})
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
		items = append(items, tabStripItem{ID: id, Label: label})
	}
	return items
}

func (m Model) hasSubAgentTabs() bool {
	return len(m.chatTabs.order) > 1
}

func (m Model) renderChatTabStrip() string {
	return renderTabStrip(m.width, m.chatTabItems(), m.chatTabs.active, false, m.styles)
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
func (m *Model) closeActiveSubAgentTab() bool {
	m.ensureChatTabs()
	id := m.chatTabs.active
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
	m.chatTabs.active = fallback
	m.activeChat().rebuild()
	return true
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
	if len(ev.tools) > 0 && ev.kind == "started" {
		view.AppendEntry(&Entry{Role: RoleSystem, Content: "Tools: " + strings.Join(ev.tools, ", ")})
	}
	if len(ev.ignored) > 0 {
		view.AppendEntry(&Entry{Role: RoleSystem, Content: "Ignored requested tools: " + strings.Join(ev.ignored, ", ")})
	}
	if ev.inner != nil {
		view.Apply(ev.inner)
	} else if ev.text != "" {
		view.AppendEntry(&Entry{Role: RoleSystem, Content: ev.text})
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
