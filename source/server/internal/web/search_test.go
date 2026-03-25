package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchResultFields(t *testing.T) {
	// Verify SearchResult struct has the expected fields
	r := SearchResult{
		URL:     "https://example.com",
		Title:   "Example",
		Snippet: "A snippet",
	}
	if r.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", r.URL, "https://example.com")
	}
	if r.Title != "Example" {
		t.Errorf("Title = %q, want %q", r.Title, "Example")
	}
	if r.Snippet != "A snippet" {
		t.Errorf("Snippet = %q, want %q", r.Snippet, "A snippet")
	}
}

func TestSearcherValidation(t *testing.T) {
	// Searcher should reject empty queries
	s := NewSearcher("/nonexistent/python3", "/nonexistent/script.py")
	_, err := s.Search(context.Background(), "", 5)
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
}

func TestSearcherMissingPython(t *testing.T) {
	// Searcher should return a clear error when venv python doesn't exist
	s := NewSearcher("/nonexistent/path/python3", "/nonexistent/script.py")
	_, err := s.Search(context.Background(), "test query", 5)
	if err == nil {
		t.Fatal("expected error for missing python binary, got nil")
	}
}

func TestSearcherMissingScript(t *testing.T) {
	// Searcher should return a clear error when the script doesn't exist.
	// Use the system python3 but point at a nonexistent script.
	s := NewSearcher("python3", "/nonexistent/ddg_search.py")
	_, err := s.Search(context.Background(), "test query", 5)
	if err == nil {
		t.Fatal("expected error for missing script, got nil")
	}
}

func TestSearcherParsesJSON(t *testing.T) {
	// Create a fake python script that outputs known JSON
	dir := t.TempDir()
	script := filepath.Join(dir, "fake_search.py")
	err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import json, sys
results = [
    {"url": "https://example.com/1", "title": "Result 1", "snippet": "First result"},
    {"url": "https://example.com/2", "title": "Result 2", "snippet": "Second result"}
]
json.dump(results, sys.stdout)
`), 0755)
	if err != nil {
		t.Fatal(err)
	}

	s := NewSearcher("python3", script)
	results, err := s.Search(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("results[0].URL = %q, want %q", results[0].URL, "https://example.com/1")
	}
	if results[1].Title != "Result 2" {
		t.Errorf("results[1].Title = %q, want %q", results[1].Title, "Result 2")
	}
}

func TestSearcherHandlesStderrError(t *testing.T) {
	// Script that writes to stderr and exits non-zero
	dir := t.TempDir()
	script := filepath.Join(dir, "error_search.py")
	err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import sys
print("search error: rate limited", file=sys.stderr)
sys.exit(1)
`), 0755)
	if err != nil {
		t.Fatal(err)
	}

	s := NewSearcher("python3", script)
	_, err = s.Search(context.Background(), "test", 5)
	if err == nil {
		t.Fatal("expected error from failing script, got nil")
	}
	// Error should include stderr content
	if got := err.Error(); !contains(got, "rate limited") {
		t.Errorf("error = %q, want it to contain 'rate limited'", got)
	}
}

func TestSearcherContextCancellation(t *testing.T) {
	// Script that sleeps forever — context cancellation should kill it
	dir := t.TempDir()
	script := filepath.Join(dir, "slow_search.py")
	err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import time
time.sleep(60)
`), 0755)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	s := NewSearcher("python3", script)
	_, err = s.Search(ctx, "test", 5)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestSearcherEmptyResults(t *testing.T) {
	// Script that returns empty array
	dir := t.TempDir()
	script := filepath.Join(dir, "empty_search.py")
	err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import json, sys
json.dump([], sys.stdout)
`), 0755)
	if err != nil {
		t.Fatal(err)
	}

	s := NewSearcher("python3", script)
	results, err := s.Search(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}

func TestSearcherMaxResultsClamping(t *testing.T) {
	// max_results <= 0 should default to 5
	dir := t.TempDir()
	script := filepath.Join(dir, "echo_args.py")
	// Script echoes its arguments so we can verify what was passed
	err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import argparse, json, sys
parser = argparse.ArgumentParser()
parser.add_argument("--query", required=True)
parser.add_argument("--max-results", type=int, default=5)
args = parser.parse_args()
# Return the max_results as a result so the test can verify
json.dump([{"url": "", "title": str(args.max_results), "snippet": ""}], sys.stdout)
`), 0755)
	if err != nil {
		t.Fatal(err)
	}

	s := NewSearcher("python3", script)
	results, err := s.Search(context.Background(), "test", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].Title != "5" {
		t.Errorf("max_results passed as %q, want '5'", results[0].Title)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
