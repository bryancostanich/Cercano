package secrets

import "testing"

func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemory()
	if err := s.Set("openai", "sk-123"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("openai")
	if err != nil || got != "sk-123" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := s.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("openai"); err == nil {
		t.Error("expected error after delete")
	}
}
