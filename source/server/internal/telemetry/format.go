package telemetry

import (
	"fmt"
	"strings"
)

const (
	barWidth    = 30 // max width of horizontal bar charts
	pieRadius   = 6  // radius of the ASCII pie chart (in lines)
	boxWidth    = 60 // width of section boxes
)

// FormatStatsASCII renders a Stats struct as a rich ASCII dashboard.
func FormatStatsASCII(stats *Stats) string {
	var out strings.Builder

	totalLocal := stats.TotalInputTokens + stats.TotalOutputTokens
	totalCloud := stats.TotalCloudInputTokens + stats.TotalCloudOutputTokens

	// Header
	out.WriteString(box("CERCANO USAGE DASHBOARD"))
	out.WriteString("\n")

	// Totals section
	out.WriteString(sectionHeader("Overview"))
	out.WriteString(fmt.Sprintf("  Total Requests:  %s\n", formatNumber(stats.TotalRequests)))
	out.WriteString(fmt.Sprintf("  Local Tokens:    %s (%s in, %s out)\n",
		formatNumber(totalLocal), formatNumber(stats.TotalInputTokens), formatNumber(stats.TotalOutputTokens)))

	if totalCloud > 0 {
		out.WriteString(fmt.Sprintf("  Cloud Tokens:    %s (%s in, %s out)\n",
			formatNumber(totalCloud), formatNumber(stats.TotalCloudInputTokens), formatNumber(stats.TotalCloudOutputTokens)))
		if stats.EstimatedNetSavings > 0 {
			savingsPct := float64(stats.EstimatedNetSavings) / float64(totalCloud+stats.EstimatedNetSavings) * 100
			out.WriteString(fmt.Sprintf("  Tokens Saved:    %s (%.1f%% of what cloud would have used)\n",
				formatNumber(stats.EstimatedNetSavings), savingsPct))
		}
		out.WriteString("\n")
		out.WriteString(localVsCloudBar(totalLocal, totalCloud))
	} else {
		out.WriteString(fmt.Sprintf("  Tokens Saved:    ~%s (estimated)\n", formatNumber(stats.LocalTokensSaved)))
	}
	out.WriteString("\n")

	// Estimated Cloud Savings
	if stats.TotalContentAvoided > 0 {
		overhead := stats.TotalContentAvoided - stats.EstimatedNetSavings
		out.WriteString(sectionHeader("Estimated Cloud Savings"))
		out.WriteString(fmt.Sprintf("  Content kept out of cloud:  %s tokens\n", formatNumber(stats.TotalContentAvoided)))
		out.WriteString(fmt.Sprintf("  Cercano overhead:          -%s tokens\n", formatNumber(overhead)))
		out.WriteString("  ─────────────────────────────────────\n")
		out.WriteString(fmt.Sprintf("  Estimated net savings:     %s tokens\n", formatNumber(stats.EstimatedNetSavings)))
		out.WriteString("\n")
	}

	// By Tool - horizontal bar chart
	if len(stats.ByTool) > 0 {
		out.WriteString(sectionHeader("By Tool"))
		maxTokens := 0
		for _, t := range stats.ByTool {
			total := t.InputTokens + t.OutputTokens
			if total > maxTokens {
				maxTokens = total
			}
		}
		for _, t := range stats.ByTool {
			total := t.InputTokens + t.OutputTokens
			name := strings.TrimPrefix(t.Name, "cercano_")
			out.WriteString(fmt.Sprintf("  %-12s %s %s (%d calls)\n",
				name, horizontalBar(total, maxTokens), formatNumber(total), t.Count))
		}
		out.WriteString("\n")
	}

	// By Model
	if len(stats.ByModel) > 0 {
		out.WriteString(sectionHeader("By Model"))
		maxTokens := 0
		for _, m := range stats.ByModel {
			total := m.InputTokens + m.OutputTokens
			if total > maxTokens {
				maxTokens = total
			}
		}
		for _, m := range stats.ByModel {
			total := m.InputTokens + m.OutputTokens
			out.WriteString(fmt.Sprintf("  %-16s %s %s (%d calls)\n",
				m.Name, horizontalBar(total, maxTokens), formatNumber(total), m.Count))
		}
		out.WriteString("\n")
	}

	// By Day - sparkline-style activity chart
	if len(stats.ByDay) > 0 {
		out.WriteString(sectionHeader("Recent Activity (last 7 days)"))
		limit := len(stats.ByDay)
		if limit > 7 {
			limit = 7
		}
		maxTokens := 0
		for _, d := range stats.ByDay[:limit] {
			total := d.InputTokens + d.OutputTokens
			if total > maxTokens {
				maxTokens = total
			}
		}
		for _, d := range stats.ByDay[:limit] {
			total := d.InputTokens + d.OutputTokens
			out.WriteString(fmt.Sprintf("  %s  %s %s (%d calls)\n",
				d.Name, horizontalBar(total, maxTokens), formatNumber(total), d.Count))
		}
		out.WriteString("\n")
	}

	// By Session
	if len(stats.BySession) > 0 {
		out.WriteString(sectionHeader("Sessions"))
		limit := len(stats.BySession)
		if limit > 10 {
			limit = 10
		}
		maxTokens := 0
		for _, sess := range stats.BySession[:limit] {
			total := sess.InputTokens + sess.OutputTokens
			if total > maxTokens {
				maxTokens = total
			}
		}
		for _, sess := range stats.BySession[:limit] {
			total := sess.InputTokens + sess.OutputTokens
			timestamp := sess.StartedAt.Format("2006-01-02 15:04")
			out.WriteString(fmt.Sprintf("  %s  %s %s (%d calls)\n",
				timestamp, horizontalBar(total, maxTokens), formatNumber(total), sess.Count))
		}
		out.WriteString("\n")
	}

	return out.String()
}

// box draws a centered title in an ASCII box.
func box(title string) string {
	padding := (boxWidth - len(title) - 2) / 2
	if padding < 1 {
		padding = 1
	}
	top := "╔" + strings.Repeat("═", boxWidth-2) + "╗"
	mid := "║" + strings.Repeat(" ", padding) + title + strings.Repeat(" ", boxWidth-2-padding-len(title)) + "║"
	bot := "╚" + strings.Repeat("═", boxWidth-2) + "╝"
	return top + "\n" + mid + "\n" + bot + "\n"
}

// sectionHeader draws a section header with a line.
func sectionHeader(title string) string {
	line := strings.Repeat("─", boxWidth-len(title)-3)
	return fmt.Sprintf("┌ %s %s\n", title, line)
}

// horizontalBar draws a proportional ASCII bar.
func horizontalBar(value, maxValue int) string {
	if maxValue == 0 {
		return strings.Repeat("░", barWidth)
	}
	filled := int(float64(value) / float64(maxValue) * float64(barWidth))
	if filled < 1 && value > 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}

// localVsCloudBar draws a proportional bar showing local vs cloud split.
func localVsCloudBar(local, cloud int) string {
	total := local + cloud
	if total == 0 {
		return ""
	}
	pct := float64(local) / float64(total) * 100
	localWidth := int(pct / 100 * float64(barWidth))
	if localWidth < 1 && local > 0 {
		localWidth = 1
	}
	cloudWidth := barWidth - localWidth

	var out strings.Builder
	out.WriteString(fmt.Sprintf("  Local vs Cloud:  %.1f%% local\n", pct))
	out.WriteString(fmt.Sprintf("  [%s%s]\n",
		strings.Repeat("▓", localWidth),
		strings.Repeat("░", cloudWidth)))
	out.WriteString(fmt.Sprintf("   %s local  %s cloud\n",
		"▓", "░"))
	return out.String()
}

// formatNumber adds comma separators to a number.
func formatNumber(n int) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		result.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if result.Len() > 0 {
			result.WriteString(",")
		}
		result.WriteString(s[i : i+3])
	}
	return result.String()
}
