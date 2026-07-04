package server

import "testing"

// Enrolled download records must carry Family: ListRuntimeModels
// dedupes online-catalog entries against the inventory by family name,
// so a family-less record renders as a duplicate of its own catalog
// entry (the bug: "nomic-embed-text" appeared twice while downloading).
func TestEnrollmentRecord_CarriesFamilyAndDisplayName(t *testing.T) {
	rec := enrollmentRecord("llama_server:online:nomic-embed-text", "llama_server", "nomic-embed-text:latest")
	if rec.Family != "nomic-embed-text" {
		t.Errorf("Family = %q, want nomic-embed-text", rec.Family)
	}
	if rec.DisplayName != "Nomic Embed Text" {
		t.Errorf("DisplayName = %q", rec.DisplayName)
	}
	if rec.OllamaRef != "nomic-embed-text:latest" || rec.DownloadState != "not_downloaded" {
		t.Errorf("record = %+v", rec)
	}
}

func TestEnrollmentRecord_TaglessRef(t *testing.T) {
	rec := enrollmentRecord("id", "llama_server", "qwen2.5-coder")
	if rec.Family != "qwen2.5-coder" {
		t.Errorf("Family = %q", rec.Family)
	}
}
