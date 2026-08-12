package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

// fakeVision is a controllable VisionService for the inspect_image skeleton.
type fakeVision struct {
	available bool
	present   map[string]bool // (convID+"|"+imageID) -> true
	answer    capabilities.VisionAnswer
	err       error

	inspectCalls int
}

func (f *fakeVision) Available() bool { return f.available }
func (f *fakeVision) Lookup(convID, imageID string) bool {
	return f.present[convID+"|"+imageID]
}
func (f *fakeVision) Inspect(_ context.Context, _, _, _ string) (capabilities.VisionAnswer, error) {
	f.inspectCalls++
	if f.err != nil {
		return capabilities.VisionAnswer{}, f.err
	}
	return f.answer, nil
}

func inspectCall(convID string, args map[string]any, vs capabilities.VisionService) *capabilities.Call {
	raw, _ := json.Marshal(args)
	return &capabilities.Call{
		Args:           raw,
		ConversationID: convID,
		Svc:            capabilities.Services{Vision: vs},
	}
}

func TestInspectImage_Metadata(t *testing.T) {
	cap := InspectImage()
	if cap.Name() != "inspect_image" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	// Agent-only: the attachment store is a live agent-session concept.
	s := cap.Surfaces()
	if !s.Has(capabilities.SurfaceAgent) || s.Has(capabilities.SurfaceMCP) {
		t.Fatalf("inspect_image should be agent-only, got %v", s)
	}
}

func TestInspectImage_Success(t *testing.T) {
	vs := &fakeVision{
		available: true,
		present:   map[string]bool{"c1|img_abc_1": true},
		answer: capabilities.VisionAnswer{
			Answer:     "A red square.",
			Confidence: "high",
			Source:     "open:gemma-3-4b-it",
		},
	}
	res, err := InspectImage().Execute(context.Background(),
		inspectCall("c1", map[string]any{"image_id": "img_abc_1", "question": "what color?"}, vs))
	if err != nil {
		t.Fatal(err)
	}
	if vs.inspectCalls != 1 {
		t.Fatalf("expected 1 inspect call, got %d", vs.inspectCalls)
	}
	body := res.Text
	for _, want := range []string{"img_abc_1", "what color?", "A red square.", "high", "open:gemma-3-4b-it"} {
		if !strings.Contains(body, want) {
			t.Errorf("envelope missing %q:\n%s", want, body)
		}
	}
}

func TestInspectImage_OmitsEmptyConfidenceAndSource(t *testing.T) {
	vs := &fakeVision{
		available: true,
		present:   map[string]bool{"c1|img_abc_1": true},
		answer:    capabilities.VisionAnswer{Answer: "Just an answer."},
	}
	res, err := InspectImage().Execute(context.Background(),
		inspectCall("c1", map[string]any{"image_id": "img_abc_1", "question": "q?"}, vs))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "Confidence:") || strings.Contains(res.Text, "Source:") {
		t.Fatalf("empty confidence/source should be omitted:\n%s", res.Text)
	}
}

func TestInspectImage_Unavailable(t *testing.T) {
	// Nil vision service: unavailable result, not an error, no crash.
	res, err := InspectImage().Execute(context.Background(),
		inspectCall("c1", map[string]any{"image_id": "img_abc_1", "question": "q?"}, nil))
	if err != nil {
		t.Fatalf("nil vision must be a graceful result, got err %v", err)
	}
	if !strings.Contains(res.Text, "Vision is not available") {
		t.Fatalf("want unavailable message, got:\n%s", res.Text)
	}

	// Wired but not available (no vision model configured).
	vs := &fakeVision{available: false}
	res, err = InspectImage().Execute(context.Background(),
		inspectCall("c1", map[string]any{"image_id": "img_abc_1", "question": "q?"}, vs))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Vision is not available") {
		t.Fatalf("want unavailable message, got:\n%s", res.Text)
	}
	if vs.inspectCalls != 0 {
		t.Fatalf("unavailable must not call Inspect, got %d calls", vs.inspectCalls)
	}
}

func TestInspectImage_StaleOrUnknownID(t *testing.T) {
	vs := &fakeVision{available: true, present: map[string]bool{}} // nothing stored
	res, err := InspectImage().Execute(context.Background(),
		inspectCall("c1", map[string]any{"image_id": "img_gone_9", "question": "q?"}, vs))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "no longer available") || !strings.Contains(res.Text, "reattach") {
		t.Fatalf("want reattach message, got:\n%s", res.Text)
	}
	if vs.inspectCalls != 0 {
		t.Fatalf("stale id must not call Inspect, got %d calls", vs.inspectCalls)
	}
}

func TestInspectImage_ProviderError(t *testing.T) {
	vs := &fakeVision{
		available: true,
		present:   map[string]bool{"c1|img_abc_1": true},
		err:       errors.New("vision timeout"),
	}
	res, err := InspectImage().Execute(context.Background(),
		inspectCall("c1", map[string]any{"image_id": "img_abc_1", "question": "q?"}, vs))
	if err != nil {
		t.Fatalf("provider error must be a graceful result, got err %v", err)
	}
	if !strings.Contains(res.Text, "Could not inspect image") || !strings.Contains(res.Text, "vision timeout") {
		t.Fatalf("want graceful provider-error message, got:\n%s", res.Text)
	}
}

func TestInspectImage_InvalidArgs(t *testing.T) {
	vs := &fakeVision{available: true, present: map[string]bool{"c1|img_abc_1": true}}
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing image_id", map[string]any{"question": "q?"}, "image_id is required"},
		{"blank image_id", map[string]any{"image_id": "  ", "question": "q?"}, "image_id is required"},
		{"missing question", map[string]any{"image_id": "img_abc_1"}, "question is required"},
		{"blank question", map[string]any{"image_id": "img_abc_1", "question": "   "}, "question is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := InspectImage().Execute(context.Background(), inspectCall("c1", c.args, vs))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error %q, got %v", c.want, err)
			}
		})
	}
	if vs.inspectCalls != 0 {
		t.Fatalf("invalid args must not call Inspect, got %d calls", vs.inspectCalls)
	}
}
