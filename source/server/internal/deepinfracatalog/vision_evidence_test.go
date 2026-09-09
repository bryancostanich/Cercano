package deepinfracatalog

import (
	"context"
	"testing"

	"cercano/source/server/internal/catalog"
)

// The index publishes no supports_vision field; the "multimodal" tag is the
// only affirmative image-capability signal it offers. Map it in one direction
// only.
func TestList_MapsMultimodalTagToVisionEvidence(t *testing.T) {
	srv, _ := fixtureServer(t)
	rows, err := newTestSource(t, srv).List(context.Background(), catalog.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = r.SupportsVision
	}
	// Tagged multimodal, tools-capable, live.
	if !got["anthropic/claude-sonnet-4-6"] {
		t.Error("multimodal-tagged model reported no vision evidence")
	}
	// Tools-capable and live, but no multimodal tag: evidence absent.
	if got["Qwen/Qwen3.8-2.4T-A95B"] {
		t.Error("untagged model reported vision evidence")
	}
}

func TestDetail_CarriesVisionEvidence(t *testing.T) {
	srv, _ := fixtureServer(t)
	src := newTestSource(t, srv)
	d, err := src.Detail(context.Background(), "anthropic/claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if !d.SupportsVision {
		t.Error("detail dropped vision evidence")
	}
	d, err = src.Detail(context.Background(), "Qwen/Qwen3.8-2.4T-A95B")
	if err != nil {
		t.Fatal(err)
	}
	if d.SupportsVision {
		t.Error("detail invented vision evidence for an untagged model")
	}
}

// Guard the direction of the inference: absence of the tag must not be
// recorded anywhere as a positive.
func TestVisionEvidence_IsAffirmativeOnly(t *testing.T) {
	m := wireModel{ModelName: "x/y", Type: "text-generation", Tags: []string{"tools"}}
	if toModel(m).SupportsVision {
		t.Fatal("model without multimodal tag reported vision support")
	}
	m.Tags = append(m.Tags, multimodalTag)
	if !toModel(m).SupportsVision {
		t.Fatal("model with multimodal tag reported no vision support")
	}
}
