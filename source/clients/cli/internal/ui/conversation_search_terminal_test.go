package ui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"testing"
)

func TestConversationSearchTerminalCtrlFInput(t *testing.T) {
	for _, tt := range []struct{ name, open, text, escape string }{
		{"legacy", "\x06", "needle", "\x1b"},
		{"kitty", "\x1b[102;5u", "\x1b[110;;110u\x1b[101;;101u\x1b[101;;101u\x1b[100;;100u\x1b[108;;108u\x1b[101;;101u", "\x1b[27u"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := searchModel(t)
			m.input.SetValue("draft")
			var decoder uv.EventDecoder
			deliver := func(raw string) {
				for len(raw) > 0 {
					n, event := decoder.Decode([]byte(raw))
					if n == 0 {
						t.Fatalf("no decoded event for %q", raw)
					}
					raw = raw[n:]
					key, ok := event.(uv.KeyPressEvent)
					if !ok {
						t.Fatalf("unexpected %T %+v", event, event)
					}
					t.Logf("decoded key: %+v", key)
					m = sendSearch(t, m, tea.KeyPressMsg(key))
					_ = m.View()
				}
			}
			deliver(tt.open)
			if !m.searchVisible() {
				t.Fatal("Ctrl+F did not open search")
			}
			deliver(tt.text)
			if m.search.input.Value() != "needle" {
				t.Fatalf("query=%q", m.search.input.Value())
			}
			deliver(tt.escape)
			if m.searchVisible() || m.search != nil {
				t.Fatal("Escape did not close search")
			}
			if m.input.Value() != "draft" {
				t.Fatal("search modified draft")
			}
		})
	}
}
