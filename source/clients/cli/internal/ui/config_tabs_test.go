package ui

import "testing"

func TestCycleConfigTabWrapsBothWays(t *testing.T) {
	if got := cycleConfigTab(configTabContext, +1); got != configTabGeneral {
		t.Fatalf("forward wrap: got %v, want General", got)
	}
	if got := cycleConfigTab(configTabGeneral, -1); got != configTabContext {
		t.Fatalf("backward wrap: got %v, want Context", got)
	}
	if got := cycleConfigTab(configTabGeneral, +1); got != configTabCloud {
		t.Fatalf("forward step: got %v, want Cloud", got)
	}
}

func TestClampConfigTabBounds(t *testing.T) {
	if got := clampConfigTab(-3); got != configTabGeneral {
		t.Fatalf("under: got %v, want General", got)
	}
	if got := clampConfigTab(99); got != configTabContext {
		t.Fatalf("over: got %v, want Context", got)
	}
}

// TestConfigTabAtXMatchesSegments walks every column across the strip geometry
// and asserts the hit-tester returns the same tab the layout placed there, and
// -1 in the gaps between cells. This guards the render/hit-test shared-geometry
// invariant.
func TestConfigTabAtXMatchesSegments(t *testing.T) {
	segs := configTabSegments()
	if len(segs) != configTabCount {
		t.Fatalf("segment count %d, want %d", len(segs), configTabCount)
	}
	last := segs[len(segs)-1].end
	for x := 0; x < last+3; x++ {
		want := configTab(-1)
		for _, seg := range segs {
			if x >= seg.start && x < seg.end {
				want = seg.tab
			}
		}
		if got := configTabAtX(x); got != want {
			t.Fatalf("configTabAtX(%d) = %v, want %v", x, got, want)
		}
	}
}

// TestConfigTabAtXHitsEveryTab makes sure each tab is reachable — a click on
// the first column of each cell resolves to that tab.
func TestConfigTabAtXHitsEveryTab(t *testing.T) {
	for _, seg := range configTabSegments() {
		if got := configTabAtX(seg.start); got != seg.tab {
			t.Fatalf("start of %v resolved to %v", seg.tab, got)
		}
	}
}

func TestConfigTabLabelsCoverAllTabs(t *testing.T) {
	if len(configTabLabels) != configTabCount {
		t.Fatalf("labels %d, want %d", len(configTabLabels), configTabCount)
	}
	for i := 0; i < configTabCount; i++ {
		if configTab(i).label() == "" {
			t.Fatalf("tab %d has empty label", i)
		}
	}
}
