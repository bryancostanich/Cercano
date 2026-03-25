package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
		{-500, "-500"},
	}
	for _, tc := range tests {
		got := formatNumber(tc.input)
		if got != tc.expected {
			t.Errorf("formatNumber(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestHorizontalBar(t *testing.T) {
	// Full bar
	bar := horizontalBar(100, 100)
	if !strings.Contains(bar, "█") {
		t.Error("full bar should contain filled blocks")
	}
	if strings.Contains(bar, "░") {
		t.Error("full bar should not contain empty blocks")
	}
	if len([]rune(bar)) != barWidth {
		t.Errorf("bar length should be %d runes, got %d", barWidth, len([]rune(bar)))
	}

	// Empty bar
	bar = horizontalBar(0, 100)
	if strings.Contains(bar, "█") {
		t.Error("empty bar should not contain filled blocks")
	}

	// Half bar
	bar = horizontalBar(50, 100)
	filled := strings.Count(bar, "█")
	if filled != barWidth/2 {
		t.Errorf("half bar should have %d filled, got %d", barWidth/2, filled)
	}

	// Zero max
	bar = horizontalBar(0, 0)
	if len([]rune(bar)) != barWidth {
		t.Errorf("zero-max bar should still be %d runes wide", barWidth)
	}
}

func TestBox(t *testing.T) {
	result := box("TEST TITLE")
	if !strings.Contains(result, "╔") {
		t.Error("box should have top-left corner")
	}
	if !strings.Contains(result, "TEST TITLE") {
		t.Error("box should contain title")
	}
	if !strings.Contains(result, "╝") {
		t.Error("box should have bottom-right corner")
	}
}

func TestSectionHeader(t *testing.T) {
	result := sectionHeader("Overview")
	if !strings.Contains(result, "Overview") {
		t.Error("section header should contain title")
	}
	if !strings.Contains(result, "┌") {
		t.Error("section header should have left border")
	}
	if !strings.Contains(result, "─") {
		t.Error("section header should have line")
	}
}

func TestLocalVsCloudBar(t *testing.T) {
	result := localVsCloudBar(100, 900)
	if !strings.Contains(result, "10.0%") {
		t.Error("should show correct percentage")
	}
	if !strings.Contains(result, "▓") {
		t.Error("should contain local marker")
	}
	if !strings.Contains(result, "░") {
		t.Error("should contain cloud marker")
	}

	// Zero total
	result = localVsCloudBar(0, 0)
	if result != "" {
		t.Error("zero total should return empty string")
	}
}

func TestFormatStatsASCII(t *testing.T) {
	stats := &Stats{
		TotalRequests:          24,
		TotalInputTokens:       126435,
		TotalOutputTokens:      14537,
		TotalCloudInputTokens:  30063,
		TotalCloudOutputTokens: 973521,
		LocalPercentage:        10.0,
		ByTool: []GroupStats{
			{Name: "cercano_extract", Count: 8, InputTokens: 40000, OutputTokens: 9961},
			{Name: "cercano_research", Count: 6, InputTokens: 35000, OutputTokens: 6276},
			{Name: "cercano_summarize", Count: 3, InputTokens: 8000, OutputTokens: 1012},
		},
		ByModel: []GroupStats{
			{Name: "qwen3-coder", Count: 24, InputTokens: 126435, OutputTokens: 14537},
		},
		ByDay: []GroupStats{
			{Name: "2026-03-25", Count: 17, InputTokens: 80000, OutputTokens: 13153},
			{Name: "2026-03-24", Count: 7, InputTokens: 40000, OutputTokens: 7819},
		},
		BySession: []SessionStats{
			{SessionID: "abc", StartedAt: time.Date(2026, 3, 25, 15, 6, 0, 0, time.UTC), Count: 5, InputTokens: 20000, OutputTokens: 3000},
			{SessionID: "def", StartedAt: time.Date(2026, 3, 25, 13, 3, 0, 0, time.UTC), Count: 12, InputTokens: 50000, OutputTokens: 8000},
		},
	}

	result := FormatStatsASCII(stats)

	// Check sections exist
	checks := []string{
		"CERCANO USAGE DASHBOARD",
		"Overview",
		"24",
		"By Tool",
		"extract",
		"research",
		"summarize",
		"By Model",
		"qwen3-coder",
		"Recent Activity",
		"2026-03-25",
		"Sessions",
		"█",
		"░",
		"▓",
		"Local vs Cloud",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("formatted stats should contain %q", check)
		}
	}
}

func TestFormatStatsASCII_NoCloudTokens(t *testing.T) {
	stats := &Stats{
		TotalRequests:     5,
		TotalInputTokens:  10000,
		TotalOutputTokens: 2000,
		LocalTokensSaved:  12000,
		ByTool: []GroupStats{
			{Name: "cercano_local", Count: 5, InputTokens: 10000, OutputTokens: 2000},
		},
	}

	result := FormatStatsASCII(stats)
	if !strings.Contains(result, "Tokens Saved") {
		t.Error("should show estimated savings when no cloud data")
	}
	if strings.Contains(result, "Local vs Cloud") {
		t.Error("should not show local vs cloud bar when no cloud data")
	}
}
