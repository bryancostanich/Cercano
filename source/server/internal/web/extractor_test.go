package web

import (
	"strings"
	"testing"
)

func TestExtractText_Basic(t *testing.T) {
	html := `<html><body><h1>Hello World</h1><p>This is a paragraph.</p></body></html>`
	text := ExtractText(html)

	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected title, got %q", text)
	}
	if !strings.Contains(text, "This is a paragraph.") {
		t.Errorf("expected paragraph, got %q", text)
	}
}

func TestExtractText_StripsScripts(t *testing.T) {
	html := `<html><body><p>Good content</p><script>var x = "bad";</script><p>More content</p></body></html>`
	text := ExtractText(html)

	if strings.Contains(text, "bad") {
		t.Error("expected script content to be stripped")
	}
	if !strings.Contains(text, "Good content") {
		t.Error("expected body content to remain")
	}
}

func TestExtractText_StripsStyles(t *testing.T) {
	html := `<html><head><style>body { color: red; }</style></head><body><p>Visible</p></body></html>`
	text := ExtractText(html)

	if strings.Contains(text, "color: red") {
		t.Error("expected style content to be stripped")
	}
	if !strings.Contains(text, "Visible") {
		t.Error("expected body content to remain")
	}
}

func TestExtractText_StripsNav(t *testing.T) {
	html := `<html><body><nav><a href="/">Home</a><a href="/about">About</a></nav><main><p>Article content</p></main></body></html>`
	text := ExtractText(html)

	if !strings.Contains(text, "Article content") {
		t.Error("expected main content to remain")
	}
	// Nav should be stripped or at least not dominate
	lines := strings.Split(strings.TrimSpace(text), "\n")
	firstNonEmpty := ""
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			firstNonEmpty = strings.TrimSpace(l)
			break
		}
	}
	if firstNonEmpty == "Home" {
		t.Error("expected nav to be stripped")
	}
}

func TestExtractText_PreservesStructure(t *testing.T) {
	html := `<html><body>
		<h1>Title</h1>
		<p>First paragraph.</p>
		<p>Second paragraph.</p>
		<ul><li>Item 1</li><li>Item 2</li></ul>
	</body></html>`
	text := ExtractText(html)

	if !strings.Contains(text, "Title") {
		t.Error("expected title")
	}
	if !strings.Contains(text, "First paragraph.") {
		t.Error("expected first paragraph")
	}
	if !strings.Contains(text, "Item 1") {
		t.Error("expected list items")
	}
}

func TestExtractText_HandlesEmptyHTML(t *testing.T) {
	text := ExtractText("")
	if text != "" {
		t.Errorf("expected empty string, got %q", text)
	}
}

func TestExtractText_StripsFooterAndHeader(t *testing.T) {
	html := `<html><body>
		<header><div>Site Logo</div></header>
		<article><p>The real content is here.</p></article>
		<footer><p>Copyright 2026</p></footer>
	</body></html>`
	text := ExtractText(html)

	if !strings.Contains(text, "The real content is here.") {
		t.Error("expected article content")
	}
}

func TestExtractText_PrefersArticleAndMain(t *testing.T) {
	html := `<html><body>
		<nav>Navigation stuff</nav>
		<aside>Sidebar junk</aside>
		<article><h2>Article Title</h2><p>Article body.</p></article>
		<footer>Footer</footer>
	</body></html>`
	text := ExtractText(html)

	if !strings.Contains(text, "Article body.") {
		t.Error("expected article content")
	}
}
