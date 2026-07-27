package research

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// junkURLSubstrings are URL fragments that indicate a page with no research
// value: author-profile landing pages, search/home shells, auth walls, etc.
// A chased reference that resolves to one of these is dropped — this is what
// prevents Google Scholar author profiles (e.g. "scholar.google.com/citations?
// user=...") from being ingested as findings.
var junkURLSubstrings = []string{
	"scholar.google.com/citations",     // author profile
	"scholar.google.com/scholar?",      // raw search results page
	"scholar.google.",                  // any scholar.google.* landing (incl. localized)
	"/citations?user=",                 // profile anywhere
	"google.com/search",                // web search results page
	"/login",                           // auth wall
	"/signin",                          // auth wall
	"accounts.google.com",              // auth wall
	"researchgate.net/profile",         // author profile
	"linkedin.com/in/",                 // personal profile
	"twitter.com/",                     // social profile
	"x.com/",                           // social profile
}

// junkTitleSubstrings flag reference titles that are clearly a person's
// scholar/profile page rather than a document. These are matched
// case-insensitively against the cited reference title.
var junkTitleSubstrings = []string{
	"- google scholar",
	"google scholar",
	"академия google", // "Google Scholar" localized
	"- researchgate",
	"| linkedin",
}

// isJunkChaseURL reports whether a resolved URL points at a non-document page
// (profile, search shell, auth wall) that should never become a finding.
func isJunkChaseURL(rawURL string) bool {
	if rawURL == "" {
		return true
	}
	lower := strings.ToLower(rawURL)
	for _, frag := range junkURLSubstrings {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	// A bare domain root (scheme://host or scheme://host/) is a homepage, not a
	// document — reject it.
	if u, err := url.Parse(rawURL); err == nil {
		path := strings.Trim(u.Path, "/")
		if path == "" && u.RawQuery == "" {
			return true
		}
	}
	return false
}

// isJunkChaseTitle reports whether a cited reference's title looks like an
// author profile / scholar landing page rather than an actual document.
func isJunkChaseTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" {
		return true
	}
	for _, frag := range junkTitleSubstrings {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// sourceFromURL derives a human-readable source label from a URL's host,
// stripping a leading "www." and a trailing public suffix guess. This replaces
// the previous behavior of copying the *plan's* source label onto every
// finding (which mislabeled blog posts as "arXiv (CS preprints)").
func sourceFromURL(rawURL, fallback string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fallback
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	return host
}

// ChaseDecision asks the model a single focused yes/no question: is this cited
// reference worth fetching for the user's intent? It names the common junk
// patterns explicitly so the model rejects profile/search pages. Returns true
// to chase. On any error it defaults to false (skip) — the cheap failure mode
// is to under-chase, not to ingest noise.
func ChaseDecision(ctx context.Context, model ModelCaller, ref CitedReference, intent string) bool {
	prompt := fmt.Sprintf(`A research pass wants to fetch a cited reference. Decide if it is worth fetching for THIS research intent.

Research intent: %s

Cited reference:
- Title: %s
- Why it was cited: %s
- Suggested source: %s

REJECT (answer NO) if the reference is any of these:
- an author's profile or "Google Scholar" / ResearchGate / LinkedIn page
- a search-results page, homepage, or table of contents
- a login or paywall shell
- off-topic relative to the intent, or too vague to evaluate

ACCEPT (answer YES) only if it is a specific document (paper, article, spec, issue, or docs page) that plausibly adds evidence for the intent.

Answer with EXACTLY one word on the first line: YES or NO.`, intent, ref.Title, ref.Why, ref.Source)

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return false
	}
	first := strings.ToUpper(strings.TrimSpace(resp))
	// Take the first token so "YES — because..." still parses.
	if i := strings.IndexAny(first, " \n\t.—-"); i > 0 {
		first = first[:i]
	}
	return first == "YES"
}
