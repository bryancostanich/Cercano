package openai

import "testing"

func TestQuirksFor(t *testing.T) {
	if quirksFor("openai").ImagesAsBase64 {
		t.Error("openai should pass image URLs through (ImagesAsBase64=false)")
	}
	if g := quirksFor("gemini"); !g.ImagesAsBase64 || !g.NormalizeErrors {
		t.Errorf("gemini needs base64 images + error normalization, got %+v", g)
	}
	for _, b := range []string{"", "nonsense", "groq"} {
		q := quirksFor(b)
		if !q.ImagesAsBase64 || !q.NormalizeErrors {
			t.Errorf("quirksFor(%q) should be defensive, got %+v", b, q)
		}
		if q.Retry.MaxAttempts < 2 || len(q.Retry.OnStatus) == 0 {
			t.Errorf("quirksFor(%q) should retry, got %+v", b, q.Retry)
		}
	}
}
