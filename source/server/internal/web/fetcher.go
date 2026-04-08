package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultTimeout is the HTTP request timeout.
	DefaultTimeout = 30 * time.Second
	// MaxResponseSize is the maximum response body to read (5MB).
	MaxResponseSize = 5 * 1024 * 1024
	// UserAgent identifies Cercano's fetcher. Uses a browser-like User-Agent
	// to avoid bot detection that serves minimal or empty HTML.
	UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
)

// FetchResult contains the fetched and extracted content from a URL.
type FetchResult struct {
	URL         string
	Title       string
	Content     string
	ContentType string
	StatusCode  int
}

// Fetcher retrieves web pages and extracts readable text.
type Fetcher struct {
	client *http.Client
}

// NewFetcher creates a new Fetcher with default settings.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// Fetch retrieves a URL and returns extracted text content.
func (f *Fetcher) Fetch(url string) (*FetchResult, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	contentType := resp.Header.Get("Content-Type")

	// Read response body up to max size
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Sanitize to valid UTF-8 — raw HTTP responses may contain invalid
	// byte sequences that would cause gRPC marshaling failures.
	content := strings.ToValidUTF8(string(body), "\uFFFD")

	// Extract text if HTML
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		content = ExtractText(content)
	}

	return &FetchResult{
		URL:         url,
		Content:     content,
		ContentType: contentType,
		StatusCode:  resp.StatusCode,
	}, nil
}
