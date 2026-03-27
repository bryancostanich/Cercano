package research

// SourceRegistry holds all known research sources organized by category.
var SourceRegistry = []SourceEntry{
	// Academic & Scientific
	{Name: "PubMed", Type: "api", Site: "", Category: "academic", Description: "Biomedical, clinical research"},
	{Name: "arXiv", Type: "api", Site: "", Category: "academic", Description: "ML, physics, math, CS preprints"},
	{Name: "bioRxiv", Type: "web", Site: "biorxiv.org", Category: "academic", Description: "Biology preprints"},
	{Name: "Google Scholar", Type: "web", Site: "scholar.google.com", Category: "academic", Description: "Broad academic coverage"},
	{Name: "ClinicalTrials.gov", Type: "api", Site: "", Category: "academic", Description: "Clinical trials"},
	{Name: "IEEE Xplore", Type: "web", Site: "ieeexplore.ieee.org", Category: "academic", Description: "Engineering, electronics"},
	{Name: "SSRN", Type: "web", Site: "ssrn.com", Category: "academic", Description: "Social sciences, economics, law"},
	{Name: "Semantic Scholar", Type: "web", Site: "semanticscholar.org", Category: "academic", Description: "Cross-discipline, citation graph"},

	// Industry, Technology & Engineering
	{Name: "GitHub", Type: "web", Site: "github.com", Category: "technology", Description: "Open source implementations"},
	{Name: "Hacker News", Type: "web", Site: "news.ycombinator.com", Category: "technology", Description: "Tech community discussion"},
	{Name: "Stack Overflow", Type: "web", Site: "stackoverflow.com", Category: "technology", Description: "Technical Q&A"},
	{Name: "Patents", Type: "web", Site: "patents.google.com", Category: "technology", Description: "IP landscape, prior art"},

	// News, Journalism & Popular Science
	{Name: "Wired", Type: "web", Site: "wired.com", Category: "news", Description: "Technology journalism"},
	{Name: "Ars Technica", Type: "web", Site: "arstechnica.com", Category: "news", Description: "Deep technical reporting"},
	{Name: "Popular Science", Type: "web", Site: "popsci.com", Category: "news", Description: "Accessible science reporting"},
	{Name: "MIT Technology Review", Type: "web", Site: "technologyreview.com", Category: "news", Description: "Emerging tech analysis"},
	{Name: "Nature News", Type: "web", Site: "nature.com", Category: "news", Description: "Science news from Nature"},
	{Name: "The Atlantic", Type: "web", Site: "theatlantic.com", Category: "news", Description: "Long-form analysis"},
	{Name: "New York Times", Type: "web", Site: "nytimes.com", Category: "news", Description: "Broad news coverage"},

	// Reference & Encyclopedic
	{Name: "Wikipedia", Type: "web", Site: "wikipedia.org", Category: "reference", Description: "Background context, terminology"},
	{Name: "Britannica", Type: "web", Site: "britannica.com", Category: "reference", Description: "Authoritative overviews"},
	{Name: "Stanford Encyclopedia of Philosophy", Type: "web", Site: "plato.stanford.edu", Category: "reference", Description: "Philosophy, ethics, theory"},

	// Regulatory & Government
	{Name: "FDA", Type: "web", Site: "fda.gov", Category: "regulatory", Description: "Drug/device regulatory status"},
	{Name: "WHO", Type: "web", Site: "who.int", Category: "regulatory", Description: "Global health policy"},
	{Name: "NIH", Type: "web", Site: "nih.gov", Category: "regulatory", Description: "US health research, funding"},
}

// SourceEntry is a known source in the registry.
type SourceEntry struct {
	Name        string
	Type        string // "api" or "web"
	Site        string // domain for site-scoped DDG search
	Category    string // academic, technology, news, reference, regulatory
	Description string
}

// SourceNames returns just the names from the registry for prompt construction.
func SourceNames() string {
	var names string
	for i, s := range SourceRegistry {
		if i > 0 {
			names += ", "
		}
		names += s.Name + " (" + s.Description + ")"
	}
	return names
}

// FindSource looks up a source entry by name (case-insensitive match).
func FindSource(name string) *SourceEntry {
	for i := range SourceRegistry {
		if equalFold(SourceRegistry[i].Name, name) {
			return &SourceRegistry[i]
		}
	}
	return nil
}

// equalFold is a simple case-insensitive comparison.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
