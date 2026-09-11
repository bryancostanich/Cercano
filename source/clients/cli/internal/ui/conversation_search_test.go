package ui

import (
	"cercano/source/clients/cli/internal/theme"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func searchModel(t *testing.T) Model {
	t.Helper()
	m := minimalModel()
	p := theme.Cracker()
	m.palette = p
	m.styles = theme.NewStyles(p)
	m.input = newPromptInput()
	m.input.Focus()
	m.setMainChat(newChatView(m.styles, p, "", "", 78, 20))
	m.width = 80
	m.height = 30
	m.splashShown = false
	m.mainChat().SetEntries([]*Entry{
		{Role: RoleUser, Content: "Find the needle and another NEEDLE."},
		{Role: RoleAssistant, Content: "A **needle** in readable text.\n\n```go\nneedle := \"世界\"\n```"},
		{Role: RoleSystem, Content: "needle status"},
		{Role: RoleAssistant, Tool: &ToolEntry{ToolName: "needle", FullResult: "needle tool result"}},
	})
	m.relayout()
	return m
}
func TestConversationSearchScopeAndNavigation(t *testing.T) {
	m := searchModel(t)
	m.input.SetValue("unsent draft")
	top, height := m.scrollbarTop, m.mainChat().Height()
	m.openConversationSearch("needle")
	if n := len(m.search.matches); n != 4 {
		t.Fatalf("matches=%d want 4; lines=%q", n, m.mainChat().PlainLines())
	}
	if m.scrollbarTop != top+1 || m.mainChat().Height() != height-1 {
		t.Fatal("search row must reserve one row")
	}
	parts, _ := m.composeFrame()
	if !strings.Contains(ansi.Strip(parts[1]), "Find:") {
		t.Fatalf("search not below title: %q", parts)
	}
	for i := 0; i < 4; i++ {
		m = send(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if m.search.active != 0 {
		t.Fatal("next must wrap")
	}
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if m.search.active != 3 {
		t.Fatal("previous must wrap")
	}
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search != nil || m.input.Value() != "unsent draft" || m.scrollbarTop != top {
		t.Fatal("close must restore layout and preserve draft")
	}
	m.openConversationSearch("**")
	if len(m.search.matches) != 0 {
		t.Fatal("Markdown syntax must not match")
	}
	m.openConversationSearch("世界")
	if len(m.search.matches) != 1 {
		t.Fatal("code/Unicode text must match")
	}
}
func TestConversationSearchApprovalAndPaste(t *testing.T) {
	m := searchModel(t)
	pending := &confirmRequest{}
	m.pendingConfirm = pending
	m.input.SetValue("draft")
	m = send(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if m.search == nil {
		t.Fatal("Ctrl+F must work during approval")
	}
	for _, r := range "yncd" {
		m = send(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = send(t, m, tea.PasteMsg{Content: " needle"})
	if m.search.input.Value() != "yncd needle" || m.pendingConfirm != pending || m.input.Value() != "draft" {
		t.Fatal("search input must not leak to approval or composer")
	}
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.pendingConfirm != pending {
		t.Fatal("close must preserve pending approval")
	}
}
func TestConversationSearchHistoryAndStreaming(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("needle")
	m.search.jump(2)
	selected := m.search.matches[m.search.active].entry
	m.resumeBackfilling = true
	if !strings.Contains(m.searchStatus(), "Loading history…") {
		t.Fatal("missing loading indicator")
	}
	older := &Entry{Role: RoleUser, Content: "older needle"}
	m.mainChat().SetEntries(append([]*Entry{older}, m.mainChat().entries...))
	m.refreshViewport()
	if len(m.search.matches) != 5 || m.search.matches[m.search.active].entry != selected {
		t.Fatal("history prepend lost active message")
	}
	m.resumeBackfilling = false
	if strings.Contains(m.searchStatus(), "Loading") {
		t.Fatal("stale loading indicator")
	}
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "streamed needle", Streaming: true})
	m.refreshViewport()
	if len(m.search.matches) != 6 {
		t.Fatal("streamed text missing")
	}
	m.convID = "different"
	m.refreshViewport()
	if m.search != nil {
		t.Fatal("conversation replacement must reset search")
	}
}
func TestConversationSearchWrappedPhrase(t *testing.T) {
	m := searchModel(t)
	m.width = 35
	m.mainChat().SetEntries([]*Entry{{Role: RoleAssistant, Content: "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"}})
	m.relayout()
	m.openConversationSearch("delta epsilon zeta")
	if len(m.search.matches) != 1 {
		t.Fatalf("wrapped phrase not found: %q", m.mainChat().PlainLines())
	}
	m.width = 80
	m.relayout()
	if len(m.search.matches) != 1 {
		t.Fatal("resize changed match count")
	}
}
func TestConversationSearchHighlightAndMouse(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("needle")
	match := m.search.matches[0]
	span := match.spans[0]
	line, _ := m.mainChat().layout.lineAt(span.row)
	highlighted := m.mainChat().renderSearchOnLine(line, span.row)
	if highlighted == line || ansi.Strip(highlighted) != ansi.Strip(line) || ansi.StringWidth(highlighted) != ansi.StringWidth(line) {
		t.Fatal("highlight must change styling only")
	}
	m.mainChat().SetYOffset(0)
	y := m.scrollbarTop + span.row
	m = send(t, m, tea.MouseClickMsg{X: span.from, Y: y, Button: tea.MouseLeft})
	m = send(t, m, tea.MouseMotionMsg{X: span.to, Y: y, Button: tea.MouseLeft})
	m = send(t, m, tea.MouseReleaseMsg{X: span.to, Y: y, Button: tea.MouseLeft})
	if m.selectionNotice != "copied selection" || m.mainChat().selectedText() != "needle" {
		t.Fatalf("selection broken: %q %q", m.selectionNotice, m.mainChat().selectedText())
	}
}
func TestConversationSearchSlashDuringHistoryLoad(t *testing.T) {
	m := searchModel(t)
	m.resumeHydrating = true
	next, _ := m.submit("/search needle", nil)
	m = next.(Model)
	if m.search == nil || len(m.search.matches) != 4 {
		t.Fatal("slash search should work while history loads")
	}
}
func TestConversationSearchNarrowLayout(t *testing.T) {
	m := searchModel(t)
	m.width = 25
	m.resumeBackfilling = true
	m.relayout()
	m.openConversationSearch(strings.Repeat("long", 30))
	line := m.renderConversationSearch()
	if strings.Contains(line, "\n") || ansi.StringWidth(line) > 25 {
		t.Fatalf("search row overflows: %q", line)
	}
}

func TestConversationSearchNarrowLoadingVisible(t *testing.T) {
	m := searchModel(t)
	m.width = 25
	m.resumeBackfilling = true
	m.relayout()
	m.openConversationSearch("absent")
	if !strings.Contains(ansi.Strip(m.renderConversationSearch()), "Loading") {
		t.Fatalf("loading state hidden: %q", ansi.Strip(m.renderConversationSearch()))
	}
}
func TestConversationSearchLongWordAndPhraseAcrossWrap(t *testing.T) {
	for _, role := range []Role{RoleUser, RoleAssistant} {
		for _, query := range []string{"pneumonoultramicroscopicsilicovolcanoconiosis", "epsilon zeta eta"} {
			m := searchModel(t)
			m.width = 25
			m.mainChat().SetEntries([]*Entry{{Role: role, Content: "alpha beta gamma delta epsilon zeta eta pneumonoultramicroscopicsilicovolcanoconiosis"}})
			m.relayout()
			m.openConversationSearch(query)
			if len(m.search.matches) != 1 {
				t.Errorf("role %v query %q matches %d: %q", role, query, len(m.search.matches), m.mainChat().PlainLines())
			}
		}
	}
}
func TestConversationSearchUnicodeHighlightCells(t *testing.T) {
	glyphs := projectSearchLines([]string{"  A世界 e\u0301 👩‍💻 done"}, RoleAssistant)
	for _, tt := range []struct {
		query    string
		from, to int
	}{{"世界", 3, 7}, {"e\u0301", 8, 9}, {"👩‍💻", 10, 12}} {
		matches := findSearchSpans(glyphs, tt.query)
		if len(matches) != 1 || matches[0][0].from != tt.from || matches[0][0].to != tt.to {
			t.Errorf("%q wrong cell mapping: %+v", tt.query, matches)
		}
	}
}
func TestConversationSearchNoGeneratedRules(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("───")
	if len(m.search.matches) != 0 {
		t.Fatal("code rails are not message text")
	}
}

func TestConversationSearchDoesNotInventJoinedWords(t *testing.T) {
	for _, role := range []Role{RoleUser, RoleAssistant} {
		m := searchModel(t)
		m.width = 25
		m.mainChat().SetEntries([]*Entry{{Role: role, Content: "alpha beta gamma delta epsilon zeta eta theta"}})
		m.relayout()
		for _, q := range []string{"gammadelta", "epsilonzeta", "etathet"} {
			m.openConversationSearch(q)
			if len(m.search.matches) != 0 {
				t.Errorf("invented match for %q role %v: %q", q, role, m.mainChat().PlainLines())
			}
		}
	}
}

func TestConversationSearchScrollbarAndModal(t *testing.T) {
	m := searchModel(t)
	m.mainChat().SetEntries([]*Entry{{Role: RoleUser, Content: strings.Repeat("needle long history\n", 100)}})
	m.relayout()
	m.openConversationSearch("needle")
	pending := &confirmRequest{}
	m.pendingConfirm = pending
	bar := m.mainChat().Width() + 1
	m = send(t, m, tea.MouseClickMsg{X: bar, Y: m.scrollbarTop, Button: tea.MouseLeft})
	if !m.mainChat().ScrollbarDragging() {
		t.Fatal("search blocked scrollbar grab")
	}
	m = send(t, m, tea.MouseMotionMsg{X: bar, Y: m.scrollbarTop + 10, Button: tea.MouseLeft})
	m = send(t, m, tea.MouseReleaseMsg{X: bar, Y: m.scrollbarTop + 10, Button: tea.MouseLeft})
	if m.mainChat().YOffset() == 0 || m.mainChat().ScrollbarDragging() || m.pendingConfirm != pending {
		t.Fatal("search blocked scrollbar navigation")
	}
	offset := m.mainChat().YOffset()
	m = send(t, m, tea.MouseWheelMsg{X: 5, Y: m.scrollbarTop + 2, Button: tea.MouseWheelUp})
	if m.mainChat().YOffset() >= offset {
		t.Fatal("search blocked mouse wheel scrolling")
	}
	m.openRuntimeModal = &openRuntimeInstallModal{}
	if _, _, handled := m.handleConversationSearch(tea.KeyPressMsg{Code: 'n', Text: "n"}); handled {
		t.Fatal("search stole modal input")
	}
}
func TestConversationSearchOffscreenJump(t *testing.T) {
	m := searchModel(t)
	m.mainChat().SetEntries([]*Entry{{Role: RoleUser, Content: strings.Repeat("unrelated text\n", 80) + "last needle"}})
	m.relayout()
	m.mainChat().SetYOffset(0)
	m.openConversationSearch("needle")
	row := m.search.matches[0].spans[0].row
	if row < m.mainChat().YOffset() || row >= m.mainChat().YOffset()+m.mainChat().Height() {
		t.Fatal("offscreen match was not brought into view")
	}
}
func TestConversationSearchStreamingActivityExcluded(t *testing.T) {
	m := searchModel(t)
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "hello", Streaming: true})
	m.relayout()
	m.openConversationSearch("⟳")
	if len(m.search.matches) != 0 {
		t.Fatal("streaming activity is not message text")
	}
}

func TestConversationSearchTableCellAcrossResize(t *testing.T) {
	m := searchModel(t)
	m.mainChat().SetEntries([]*Entry{{Role: RoleAssistant, Content: "| Name | Value |\n|---|---|\n| key | needle |\n"}})
	for _, width := range []int{100, 35, 80} {
		m.width = width
		m.relayout()
		m.openConversationSearch("needle")
		if len(m.search.matches) != 1 {
			t.Fatalf("width %d: expected one table-cell match, got %d: %q", width, len(m.search.matches), m.mainChat().PlainLines())
		}
	}
}
