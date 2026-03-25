package web

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ExtractText converts HTML into readable plain text.
// Strips scripts, styles, nav, footer, header, and aside elements.
// Prefers content from <article> or <main> if present.
func ExtractText(html string) string {
	if html == "" {
		return ""
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	// Remove unwanted elements
	doc.Find("script, style, noscript, svg, img, video, audio, iframe, object, embed").Remove()
	doc.Find("nav, footer, header, aside").Remove()

	// Prefer <article> or <main> if present
	content := doc.Find("article")
	if content.Length() == 0 {
		content = doc.Find("main")
	}
	if content.Length() == 0 {
		content = doc.Find("body")
	}
	if content.Length() == 0 {
		return ""
	}

	var sb strings.Builder
	extractNode(content, &sb)

	// Clean up excessive whitespace
	text := sb.String()
	text = collapseWhitespace(text)
	return strings.TrimSpace(text)
}

// extractNode recursively extracts text from a goquery selection,
// adding newlines for block-level elements to preserve structure.
func extractNode(s *goquery.Selection, sb *strings.Builder) {
	s.Children().Each(func(i int, child *goquery.Selection) {
		tag := goquery.NodeName(child)

		switch tag {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			sb.WriteString("\n\n")
			sb.WriteString(strings.TrimSpace(child.Text()))
			sb.WriteString("\n\n")
		case "p", "div", "section", "article", "main":
			text := strings.TrimSpace(child.Text())
			if text != "" {
				sb.WriteString("\n\n")
				sb.WriteString(text)
			}
		case "li":
			sb.WriteString("\n- ")
			sb.WriteString(strings.TrimSpace(child.Text()))
		case "br":
			sb.WriteString("\n")
		case "pre", "code":
			sb.WriteString("\n```\n")
			sb.WriteString(child.Text())
			sb.WriteString("\n```\n")
		default:
			// For inline elements, just recurse
			if child.Children().Length() > 0 {
				extractNode(child, sb)
			} else {
				text := strings.TrimSpace(child.Text())
				if text != "" {
					sb.WriteString(text)
					sb.WriteString(" ")
				}
			}
		}
	})
}

// collapseWhitespace reduces multiple blank lines to at most two newlines.
func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	blankCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blankCount++
			if blankCount <= 1 {
				result = append(result, "")
			}
		} else {
			blankCount = 0
			result = append(result, trimmed)
		}
	}
	return strings.Join(result, "\n")
}
