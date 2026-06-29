package openai

import (
	"reflect"
	"testing"
)

func TestNewClientResolvesQuirks(t *testing.T) {
	c := NewClient(Config{Backend: "gemini", Model: "gemini-2.5-flash"})
	if !reflect.DeepEqual(c.quirks, quirksFor("gemini")) {
		t.Errorf("client quirks = %+v, want %+v", c.quirks, quirksFor("gemini"))
	}
}

func TestNewClientDefaultQuirks(t *testing.T) {
	c := NewClient(Config{}) // empty backend → defensive default
	if !c.quirks.ImagesAsBase64 || !c.quirks.NormalizeErrors {
		t.Errorf("empty backend should get defensive quirks, got %+v", c.quirks)
	}
}
