package ui

import "cercano/source/clients/cli/internal/theme"

// configTab identifies one tab in the unified configuration surface opened by
// /config, /m, /c, and /theme. The iota order is the left-to-right render
// order of the tab strip.
type configTab int

const (
	configTabGeneral configTab = iota // routing, permissions, server, dev tools (settings form)
	configTabCloud                    // cloud-profiles editor (settings form, cloud section)
	configTabRuntime                  // runtime + open-model picker (dashboard, runtime mode)
	configTabModels                   // the runtime dashboard (local model management)
	configTabMcp                      // hosted MCP servers (dashboard + add-server popover)
	configTabUI                       // theme + accent color (settings form)
	configTabContext                  // read-only context viewer for the active conversation
)

// configTabLabels are the visible tab titles, indexed by configTab.
var configTabLabels = []string{"General", "Cloud", "Runtime", "Models", "MCP", "UI", "Context"}

// configTabCount is the number of tabs; kept as a named constant so wrap-around
// navigation and digit-jump bounds stay in one place.
const configTabCount = 7

func (t configTab) label() string {
	if int(t) < 0 || int(t) >= len(configTabLabels) {
		return ""
	}
	return configTabLabels[t]
}

type configTabSegment struct {
	tab        configTab
	start, end int
}

func configTabSegments() []configTabSegment {
	base := tabStripSegments(configTabItems())
	out := make([]configTabSegment, 0, len(base))
	for _, seg := range base {
		out = append(out, configTabSegment{tab: configTabFromID(seg.id), start: seg.start, end: seg.end})
	}
	return out
}

func configTabItems() []tabStripItem {
	items := make([]tabStripItem, len(configTabLabels))
	for i, label := range configTabLabels {
		items[i] = tabStripItem{ID: configTabID(configTab(i)), Label: label}
	}
	return items
}

func configTabID(t configTab) string { return t.label() }

func configTabFromID(id string) configTab {
	for i, label := range configTabLabels {
		if label == id {
			return configTab(i)
		}
	}
	return -1
}

// clampConfigTab bounds t into the valid range without wrapping.
func clampConfigTab(t configTab) configTab {
	if t < 0 {
		return configTabGeneral
	}
	if t >= configTabCount {
		return configTabContext
	}
	return t
}

// cycleConfigTab advances the active tab by dir (+1 / -1) with wrap-around,
// so ←/→ at either end rolls to the far side of the strip.
func cycleConfigTab(active configTab, dir int) configTab {
	return configTabFromID(cycleTabStrip(configTabItems(), configTabID(active), dir))
}

// renderConfigTabStrip draws the tab bar row. The active tab is filled in the
// accent color; inactive tabs render muted. When focused is true the tab bar
// currently owns keyboard focus (←/→ switch tabs), so the active tab gets a
// brighter, bolder treatment to signal that arrows act on it — mirroring how a
// focused form field brightens.
func renderConfigTabStrip(width int, active configTab, focused bool, s theme.Styles) string {
	active = clampConfigTab(active)
	return renderTabStrip(width, configTabItems(), configTabID(active), focused, s)
}

// configTabAtX maps a 0-based column on the tab-strip row to the tab whose cell
// contains it, or -1 when the click lands in a gap or past the last tab. The
// gap columns between cells are intentionally dead so a click has to land on a
// label to switch.
func configTabAtX(x int) configTab {
	id, ok := tabStripAtX(configTabItems(), x)
	if !ok {
		return -1
	}
	return configTabFromID(id)
}
