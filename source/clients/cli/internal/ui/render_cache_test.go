package ui

import (
	"strings"
	"testing"
	"time"
)

// The render cache must be *served* (not just populated) and must invalidate
// on every input that changes an entry's rendered block. The sentinel trick
// proves serving: poke a marker string into the cache slot and assert the next
// rebuild emits the marker — a re-render would overwrite it.

func TestEntryCacheServedAndInvalidatedOnContentChange(t *testing.T) {
	c := newTestChatView(60, 10)
	e := &Entry{Role: RoleSystem, Content: "frozen history line"}
	c.SetEntries([]*Entry{e})

	cached, ok := c.entryCache[e]
	if !ok {
		t.Fatalf("frozen system entry not cached after SetEntries")
	}
	cached.rendered = "ENTRY-SENTINEL"
	c.entryCache[e] = cached

	c.SetEntries([]*Entry{e})
	if !strings.Contains(c.layout.flattenedContent(), "ENTRY-SENTINEL") {
		t.Fatalf("unchanged frozen entry was re-rendered instead of served from cache")
	}

	e.Content = "edited line"
	c.SetEntries([]*Entry{e})
	if strings.Contains(c.layout.flattenedContent(), "ENTRY-SENTINEL") {
		t.Fatalf("stale cache served after Content changed")
	}
	if !strings.Contains(plain(c.layout.flattenedContent()), "edited line") {
		t.Fatalf("new content missing after invalidation; got:\n%s", plain(c.layout.flattenedContent()))
	}
}

func TestEntryCacheInvalidatedOnThemeGeneration(t *testing.T) {
	c := newTestChatView(60, 10)
	e := &Entry{Role: RoleSystem, Content: "themed line"}
	c.SetEntries([]*Entry{e})

	cached := c.entryCache[e]
	cached.rendered = "THEME-SENTINEL"
	c.entryCache[e] = cached

	c.stylesGen++ // what SetStyles does
	c.SetEntries([]*Entry{e})
	if strings.Contains(c.layout.flattenedContent(), "THEME-SENTINEL") {
		t.Fatalf("stale cache served across a theme generation bump")
	}
}

func TestStreamingEntryBypassesEntryCache(t *testing.T) {
	c := newTestChatView(60, 10)
	e := &Entry{Role: RoleAssistant, Content: "still going", Streaming: true}
	c.SetEntries([]*Entry{e})
	if _, ok := c.entryCache[e]; ok {
		t.Fatalf("streaming entry must not enter the frozen-entry cache")
	}
}

func TestToolGroupCacheServedAndInvalidated(t *testing.T) {
	c := newTestChatView(80, 10)
	e := &Entry{Tool: &ToolEntry{
		ToolUseID:     "t1",
		ToolName:      "Read",
		ArgsSummary:   "README.md",
		ResultSummary: "120 lines",
		Status:        ToolStatusComplete,
		Folded:        true,
	}}
	c.SetEntries([]*Entry{e})

	g, ok := c.groupCache[0]
	if !ok {
		t.Fatalf("completed tool group not cached after SetEntries")
	}
	g.block = "GROUP-SENTINEL"
	c.groupCache[0] = g

	c.SetEntries([]*Entry{e})
	if !strings.Contains(c.layout.flattenedContent(), "GROUP-SENTINEL") {
		t.Fatalf("unchanged completed tool group was re-rendered instead of served")
	}

	e.Tool.ResultSummary = "121 lines"
	c.SetEntries([]*Entry{e})
	if strings.Contains(c.layout.flattenedContent(), "GROUP-SENTINEL") {
		t.Fatalf("stale group cache served after a member field changed")
	}
}

func TestInProgressToolGroupNeverCached(t *testing.T) {
	c := newTestChatView(80, 10)
	e := &Entry{Tool: &ToolEntry{
		ToolUseID: "t1",
		ToolName:  "Bash",
		Status:    ToolStatusInProgress,
		StartedAt: time.Now(),
		Folded:    true,
	}}
	c.SetEntries([]*Entry{e})
	if _, ok := c.groupCache[0]; ok {
		t.Fatalf("in-progress (spinner-animated) tool group must bypass the cache")
	}
}

func TestPlainLinesLazyAfterRebuild(t *testing.T) {
	c := newTestChatView(60, 10)
	c.SetEntries([]*Entry{{Role: RoleSystem, Content: "copy me"}})
	if !c.plainDirty {
		t.Fatalf("SetEntries should defer the ANSI strip (plainDirty)")
	}
	lines := c.PlainLines()
	if c.plainDirty {
		t.Fatalf("PlainLines() should materialize and clear plainDirty")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "copy me") {
		t.Fatalf("lazy plain lines lost content; got:\n%s", joined)
	}
}

// The committed-prefix cache must serve stable blocks while only the tail
// grows, and rebuild the moment a new block commits (or the last block
// extends — the trailing-table instability).
func TestCommittedPrefixServedWhileTailGrows(t *testing.T) {
	c := newTestChatView(60, 10)
	e := &Entry{Role: RoleAssistant, Streaming: true,
		Content: "para one\n\npara two\n\ntail going"}
	textW := 50

	first := c.renderAssistantMarkdown(e, textW)
	if strings.TrimSpace(first) == "" {
		t.Fatalf("setup: empty render")
	}
	c.streamPrefix.prefix = "PREFIX-SENTINEL"

	e.Content += " and growing"
	out := c.renderAssistantMarkdown(e, textW)
	if !strings.HasPrefix(out, "PREFIX-SENTINEL") {
		t.Fatalf("prefix re-rendered while only the tail grew")
	}
	if !strings.Contains(plain(out), "and growing") {
		t.Fatalf("live tail missing from output")
	}

	// A blank line commits the old tail as a block → blockCount changes →
	// the sentinel must be replaced by a real render.
	e.Content += "\n\nnew tail"
	out = c.renderAssistantMarkdown(e, textW)
	if strings.Contains(out, "PREFIX-SENTINEL") {
		t.Fatalf("stale prefix served after a new block committed")
	}
	if !strings.Contains(plain(out), "para one") || !strings.Contains(plain(out), "new tail") {
		t.Fatalf("rebuilt render lost content; got:\n%s", plain(out))
	}
}

// Token deltas must not rebuild the transcript per event: they set chatDirty
// and the next progressAnimTick frame carries the repaint. The tick must stay
// armed while the stream is alive so the next batch gets a frame too.
func TestTokenDeltaCoalescedToTickFrame(t *testing.T) {
	m := newStreamTestModel()

	next, _ := m.Update(chatStreamMsg{ev: chatAssistantDeltaMsg{token: "HelloToken"}})
	m = next.(Model)
	if !m.chatDirty {
		t.Fatalf("delta should mark the transcript dirty")
	}
	if !m.animTickActive {
		t.Fatalf("delta should arm the repaint tick")
	}
	if strings.Contains(plain(m.mainChat().layout.flattenedContent()), "HelloToken") {
		t.Fatalf("delta repainted eagerly — coalescing not in effect")
	}

	next, cmd := m.Update(progressAnimTickMsg(time.Now()))
	m = next.(Model)
	if m.chatDirty {
		t.Fatalf("tick frame should flush the dirty flag")
	}
	if !strings.Contains(plain(m.mainChat().layout.flattenedContent()), "HelloToken") {
		t.Fatalf("tick frame did not carry the deferred repaint")
	}
	if cmd == nil || !m.animTickActive {
		t.Fatalf("tick must re-arm while the stream is alive")
	}
}
