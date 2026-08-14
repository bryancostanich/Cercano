package visionattach

import (
	"fmt"
	"strings"
	"testing"
)

func TestAdd_StoresAndLooksUp(t *testing.T) {
	s := NewStore()
	res := s.Add("conv1", "image/png", []byte("alpha"))
	if res.Rejected || res.Deduped || res.Attachment == nil {
		t.Fatalf("unexpected add result: %+v", res)
	}
	att := res.Attachment
	if att.Ordinal != 1 {
		t.Errorf("ordinal = %d, want 1", att.Ordinal)
	}
	if !strings.HasPrefix(att.ID, "img_") || !strings.HasSuffix(att.ID, "_1") {
		t.Errorf("id %q not shaped img_<hash>_<ord>", att.ID)
	}
	got, ok := s.Lookup("conv1", att.ID)
	if !ok || string(got.Data) != "alpha" {
		t.Fatalf("lookup failed: ok=%v att=%+v", ok, got)
	}
}

func TestAdd_DedupsIdenticalBytes(t *testing.T) {
	s := NewStore()
	a := s.Add("c", "image/png", []byte("same"))
	b := s.Add("c", "image/png", []byte("same"))
	if b.Deduped != true || b.Attachment == nil {
		t.Fatalf("second identical add should dedup: %+v", b)
	}
	if a.Attachment.ID != b.Attachment.ID {
		t.Errorf("dedup returned different IDs: %s vs %s", a.Attachment.ID, b.Attachment.ID)
	}
	if s.Count("c") != 1 {
		t.Errorf("count = %d, want 1 after dedup", s.Count("c"))
	}
}

func TestAdd_UniqueIDsForDistinctImages(t *testing.T) {
	s := NewStore()
	a := s.Add("c", "image/png", []byte("one"))
	b := s.Add("c", "image/png", []byte("two"))
	if a.Attachment.ID == b.Attachment.ID {
		t.Fatalf("distinct images share an ID: %s", a.Attachment.ID)
	}
	if a.Attachment.Ordinal != 1 || b.Attachment.Ordinal != 2 {
		t.Errorf("ordinals = %d,%d want 1,2", a.Attachment.Ordinal, b.Attachment.Ordinal)
	}
}

func TestAdd_RejectsImageCountCap(t *testing.T) {
	s := NewStore().WithCaps(2, 0)
	s.Add("c", "image/png", []byte("a"))
	s.Add("c", "image/png", []byte("b"))
	res := s.Add("c", "image/png", []byte("c"))
	if !res.Rejected || res.Attachment != nil {
		t.Fatalf("third add should be rejected by count cap: %+v", res)
	}
	if !strings.Contains(res.RejectReason, "attachment limit") {
		t.Errorf("reject reason = %q", res.RejectReason)
	}
	if s.Count("c") != 2 {
		t.Errorf("count = %d, want 2 (rejected image not stored)", s.Count("c"))
	}
}

func TestAdd_RejectsByteCap(t *testing.T) {
	s := NewStore().WithCaps(0, 8)
	if r := s.Add("c", "image/png", []byte("12345")); r.Attachment == nil {
		t.Fatalf("first add within byte cap should succeed: %+v", r)
	}
	res := s.Add("c", "image/png", []byte("6789")) // 5+4 > 8
	if !res.Rejected {
		t.Fatalf("second add should exceed byte cap: %+v", res)
	}
	if !strings.Contains(res.RejectReason, "size limit") {
		t.Errorf("reject reason = %q", res.RejectReason)
	}
}

func TestAdd_RejectsEmptyAndBlankConv(t *testing.T) {
	s := NewStore()
	if r := s.Add("", "image/png", []byte("x")); !r.Rejected {
		t.Error("blank conversation id should be rejected")
	}
	if r := s.Add("c", "image/png", nil); !r.Rejected {
		t.Error("empty image should be rejected")
	}
}

func TestConversationsAreIsolated(t *testing.T) {
	s := NewStore()
	// Distinct bytes per conversation so we can prove lookups never cross.
	a := s.Add("convA", "image/png", []byte("alpha-bytes"))
	b := s.Add("convB", "image/png", []byte("bravo-bytes"))

	// convA's ID must not resolve in convB, and each conversation resolves to
	// ITS OWN bytes — image IDs are conversation-scoped, never global.
	if _, ok := s.Lookup("convB", a.Attachment.ID); ok {
		t.Error("convB resolved convA's attachment id")
	}
	if got, ok := s.Lookup("convA", a.Attachment.ID); !ok || string(got.Data) != "alpha-bytes" {
		t.Errorf("convA lookup wrong: ok=%v data=%q", ok, got.Data)
	}
	if got, ok := s.Lookup("convB", b.Attachment.ID); !ok || string(got.Data) != "bravo-bytes" {
		t.Errorf("convB lookup wrong: ok=%v data=%q", ok, got.Data)
	}

	// Even when two conversations store byte-identical images (yielding the same
	// conversation-scoped ID string), each resolves only to its own table.
	sameA := s.Add("convA", "image/png", []byte("dup"))
	sameB := s.Add("convB", "image/png", []byte("dup"))
	if sameA.Attachment.ID != sameB.Attachment.ID {
		t.Fatalf("identical first images should share the conversation-scoped ID shape: %s vs %s",
			sameA.Attachment.ID, sameB.Attachment.ID)
	}
	if got, _ := s.Lookup("convA", sameA.Attachment.ID); string(got.Data) != "dup" {
		t.Error("convA dup lookup returned foreign bytes")
	}
}

func TestLookupAny_UniqueMatch(t *testing.T) {
	s := NewStore()
	att := s.Add("conv1", "image/png", []byte("alpha")).Attachment

	got, convID, ok, ambiguous := s.LookupAny(att.ID)
	if !ok || ambiguous || convID != "conv1" || string(got.Data) != "alpha" {
		t.Fatalf("LookupAny = att:%+v conv:%q ok:%v ambiguous:%v", got, convID, ok, ambiguous)
	}
}

func TestLookupAny_Miss(t *testing.T) {
	s := NewStore()
	s.Add("conv1", "image/png", []byte("alpha"))

	got, convID, ok, ambiguous := s.LookupAny("img_missing_1")
	if got != nil || convID != "" || ok || ambiguous {
		t.Fatalf("LookupAny miss = att:%+v conv:%q ok:%v ambiguous:%v", got, convID, ok, ambiguous)
	}
}

func TestLookupAny_Ambiguous(t *testing.T) {
	s := NewStore()
	a := s.Add("convA", "image/png", []byte("same")).Attachment
	b := s.Add("convB", "image/png", []byte("same")).Attachment
	if a.ID != b.ID {
		t.Fatalf("test setup expected duplicate conversation-scoped IDs, got %q and %q", a.ID, b.ID)
	}

	got, convID, ok, ambiguous := s.LookupAny(a.ID)
	if got != nil || convID != "" || ok || !ambiguous {
		t.Fatalf("LookupAny ambiguous = att:%+v conv:%q ok:%v ambiguous:%v", got, convID, ok, ambiguous)
	}
}

func TestLookup_MissAfterClear(t *testing.T) {
	s := NewStore()
	a := s.Add("c", "image/png", []byte("bye"))
	s.Clear("c")
	if _, ok := s.Lookup("c", a.Attachment.ID); ok {
		t.Error("lookup should miss after Clear (models a resume with lost attachments)")
	}
	if s.Count("c") != 0 {
		t.Errorf("count = %d, want 0 after clear", s.Count("c"))
	}
}

func TestLookup_UnknownID(t *testing.T) {
	s := NewStore()
	s.Add("c", "image/png", []byte("x"))
	if _, ok := s.Lookup("c", "img_deadbeef_9"); ok {
		t.Error("unknown id should miss")
	}
	if _, ok := s.Lookup("nope", "img_x_1"); ok {
		t.Error("unknown conversation should miss")
	}
}

// Sanity: IDs embed the content hash prefix so identical images across the app
// are recognizable, but ordinals keep them conversation-unique.
func TestID_ShapeStable(t *testing.T) {
	s := NewStore()
	att := s.Add("c", "image/png", []byte("pixels")).Attachment
	if want := fmt.Sprintf("img_%s_1", att.Hash[:6]); att.ID != want {
		t.Errorf("id = %q, want %q", att.ID, want)
	}
}
