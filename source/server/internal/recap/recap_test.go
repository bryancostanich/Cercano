package recap

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
)

type fakeStore struct {
	mu      sync.Mutex
	info    conversation.Info
	turns   []conversation.Turn
	recaps  []string
	updated chan string
	titles  []string
	titleCh chan string
}

func (f *fakeStore) Get(ctx context.Context, id string) (conversation.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, nil
}
func (f *fakeStore) GetTurns(ctx context.Context, id string) ([]conversation.Turn, error) {
	return f.turns, nil
}
func (f *fakeStore) UpdateRecap(ctx context.Context, id, recap string) error {
	f.mu.Lock()
	f.recaps = append(f.recaps, recap)
	f.mu.Unlock()
	f.updated <- recap
	return nil
}
func (f *fakeStore) SetGeneratedTitle(ctx context.Context, id, title string) error {
	f.mu.Lock()
	f.titles = append(f.titles, title)
	f.mu.Unlock()
	if f.titleCh != nil {
		f.titleCh <- title
	}
	return nil
}

func TestScheduleDebouncesToOneGeneration(t *testing.T) {
	calls := 0
	var cmu sync.Mutex
	complete := func(ctx context.Context, prompt string) (string, error) {
		cmu.Lock()
		calls++
		cmu.Unlock()
		return "did stuff", nil
	}
	fs := &fakeStore{
		turns:   []conversation.Turn{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}},
		updated: make(chan string, 4),
	}
	g := New(fs, complete, 30*time.Millisecond, 10)

	g.Schedule("c1")
	g.Schedule("c1")
	g.Schedule("c1")

	select {
	case got := <-fs.updated:
		if got != "did stuff" {
			t.Errorf("recap = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("recap never generated")
	}
	time.Sleep(50 * time.Millisecond)
	cmu.Lock()
	defer cmu.Unlock()
	if calls != 2 {
		t.Errorf("complete called %d times, want 2 (one recap + one title per coalesced cycle)", calls)
	}
	fs.mu.Lock()
	nTitles := len(fs.titles)
	fs.mu.Unlock()
	if nTitles != 1 {
		t.Errorf("SetGeneratedTitle called %d times, want exactly 1 per coalesced cycle", nTitles)
	}
}

func TestGenerationFailureKeepsPriorRecap(t *testing.T) {
	complete := func(ctx context.Context, prompt string) (string, error) {
		return "", context.DeadlineExceeded
	}
	fs := &fakeStore{
		turns:   []conversation.Turn{{Role: "user", Content: "hi"}},
		updated: make(chan string, 1),
	}
	g := New(fs, complete, 10*time.Millisecond, 10)
	g.Schedule("c1")
	select {
	case <-fs.updated:
		t.Fatal("UpdateRecap should not be called on generation failure")
	case <-time.After(80 * time.Millisecond):
		// success: nothing written
	}
}

func TestGeneratesTitleFromRecap(t *testing.T) {
	// The completion returns the recap line first, then the title on the
	// second call (recap pass, then title pass).
	var n int
	var cmu sync.Mutex
	complete := func(ctx context.Context, prompt string) (string, error) {
		cmu.Lock()
		defer cmu.Unlock()
		n++
		if n == 1 {
			return "implemented the context viewer and fixed the scrollbar", nil
		}
		return "Context Viewer Scrollbar Fix", nil
	}
	fs := &fakeStore{
		turns:   []conversation.Turn{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}},
		updated: make(chan string, 4),
		titleCh: make(chan string, 4),
	}
	g := New(fs, complete, 10*time.Millisecond, 10)
	g.Schedule("c1")

	select {
	case got := <-fs.titleCh:
		if got != "Context Viewer Scrollbar Fix" {
			t.Errorf("title = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no title generated")
	}
}

func TestRecapCapIs160(t *testing.T) {
	if maxRecapChars != 160 {
		t.Errorf("maxRecapChars = %d, want 160", maxRecapChars)
	}
}

func TestFirstLineCaps(t *testing.T) {
	got := firstLine("  line one\nline two  ", 100)
	if got != "line one" {
		t.Errorf("firstLine = %q", got)
	}
	if l := firstLine("abcdefghij", 4); l != "abc…" {
		t.Errorf("cap = %q", l)
	}
}
