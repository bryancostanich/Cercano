package research

import "testing"

func TestIsJunkChaseURL(t *testing.T) {
	junk := []string{
		"https://scholar.google.com/citations?user=abc123",
		"https://scholar.google.com/scholar?q=planning",
		"https://scholar.google.de/citations?user=xyz",
		"https://www.google.com/search?q=aider+architect",
		"https://accounts.google.com/signin",
		"https://example.com/login",
		"https://www.researchgate.net/profile/Jane-Doe",
		"https://linkedin.com/in/someone",
		"https://example.com",     // bare homepage
		"https://example.com/",    // bare homepage with slash
		"",                        // empty
	}
	for _, u := range junk {
		if !isJunkChaseURL(u) {
			t.Errorf("expected %q to be flagged as junk", u)
		}
	}

	good := []string{
		"https://arxiv.org/abs/2401.12345",
		"https://openhands.dev/blog/plan-mode",
		"https://github.com/gemini-cli-extensions/conductor",
		"https://www.datacamp.com/tutorial/claude-code-plan-mode",
	}
	for _, u := range good {
		if isJunkChaseURL(u) {
			t.Errorf("expected %q to be accepted, was flagged junk", u)
		}
	}
}

func TestIsJunkChaseTitle(t *testing.T) {
	junk := []string{
		"Neel Nanda - Google Scholar",
		"Hoang H. Tran - Google Scholar",
		"Академия Google",
		"Jane Doe | LinkedIn",
		"",
	}
	for _, tt := range junk {
		if !isJunkChaseTitle(tt) {
			t.Errorf("expected title %q to be flagged as junk", tt)
		}
	}

	good := []string{
		"Plan Mode in Claude Code - Think Before You Code",
		"The OpenHands Software Agent SDK",
		"Spec-Driven Development with Gemini CLI",
	}
	for _, tt := range good {
		if isJunkChaseTitle(tt) {
			t.Errorf("expected title %q to be accepted, was flagged junk", tt)
		}
	}
}

func TestSourceFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.datacamp.com/tutorial/x": "datacamp.com",
		"https://arxiv.org/abs/2401.1":        "arxiv.org",
		"https://codewithmukesh.com/blog/y":   "codewithmukesh.com",
	}
	for u, want := range cases {
		if got := sourceFromURL(u, "fallback"); got != want {
			t.Errorf("sourceFromURL(%q) = %q, want %q", u, got, want)
		}
	}
	if got := sourceFromURL("::not a url::", "fallback"); got != "fallback" {
		t.Errorf("expected fallback for bad URL, got %q", got)
	}
}
