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

type chatTabSurface struct {
	active      string
	order       []string
	tabs        map[string]*chatTab
	childCounts map[string]int
}

const mainChatTabID = "main"

func (m *Model) ensureChatTabs() {
	if m.chatTabs.tabs == nil {
		m.chatTabs.tabs = map[string]*chatTab{}
	}
	if m.chatTabs.childCounts == nil {
		m.chatTabs.childCounts = map[string]int{}
	}
	if _, ok := m.chatTabs.tabs[mainChatTabID]; !ok {
		m.chatTabs.tabs[mainChatTabID] = &chatTab{id: mainChatTabID, title: "main", view: m.chat}
		m.chatTabs.order = append([]string{mainChatTabID}, m.chatTabs.order...)
	}
	if m.chatTabs.active == "" {
		m.chatTabs.active = mainChatTabID
	}
}

func (m *Model) syncMainChatTab() {
	m.ensureChatTabs()
	m.chatTabs.tabs[mainChatTabID].view = m.chat
}

func (m *Model) syncMainChatFromTab() {
	if m.chatTabs.tabs == nil {
		return
	}
	if tab, ok := m.chatTabs.tabs[mainChatTabID]; ok {
		m.chat = tab.view
	}
}

func (m *Model) activeChatView() *chatView {
	m.ensureChatTabs()
	if tab, ok := m.chatTabs.tabs[m.chatTabs.active]; ok {
		return &tab.view
	}
	return &m.chat
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
	vpH := m.chat.Height()
	if vpH < 1 {
		vpH = 1
	}
	view := newChatView(m.styles, m.palette, m.root, m.home, vpW, vpH)
	view.AppendEntry(&Entry{Role: RoleSystem, Content: fmt.Sprintf("Sub-agent %s started", title)})
	tab := &chatTab{id: id, parentID: parentID, title: title, tools: append([]string(nil), tools...), view: view}
	m.chatTabs.tabs[id] = tab
	m.chatTabs.order = append(m.chatTabs.order, id)
	m.chatTabs.active = id
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
	m.syncMainChatTab()
	m.chatTabs.active = id
	if id == mainChatTabID {
		m.syncMainChatFromTab()
	}
	return true
}

func (m *Model) applySubAgentEvent(ev subAgentEventMsg) {
	if ev.id == "" {
		ev.id = ev.title
	}
	if ev.id == "" {
		ev.id = "sub"
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
	if ev.text != "" {
		m.chat.Apply(chatProgressMsg{note: ev.text})
		m.chat.rebuild()
	}
}
