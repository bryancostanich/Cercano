package contextmeter

import (
	"strings"
	"testing"
)

func TestDefault_CountsNonZero(t *testing.T) {
	tok := Default()
	n := tok.Count("hello world this is a test of the tokenizer")
	if n <= 0 {
		t.Errorf("expected positive token count, got %d", n)
	}
}

func TestDefault_Empty(t *testing.T) {
	if n := Default().Count(""); n != 0 {
		t.Errorf("expected 0 tokens for empty string, got %d", n)
	}
}

func TestModelMax(t *testing.T) {
	cases := []struct {
		model     string
		wantAtLeast int
	}{
		{"qwen3-coder-next:latest", 200_000}, // 256K
		{"qwen3-coder:latest", 100_000},      // 128K
		{"claude-opus-4-7", 200_000},
		{"claude-sonnet-4-6", 200_000},
		{"gemini-1.5-pro", 1_500_000},
		{"llama3.1:8b", 100_000},
		{"unknown-model", 100_000}, // default 128K
	}
	for _, c := range cases {
		got := ModelMax(c.model)
		if got < c.wantAtLeast {
			t.Errorf("ModelMax(%q) = %d, want at least %d", c.model, got, c.wantAtLeast)
		}
	}
}

func TestCounter_AddAndUsed(t *testing.T) {
	c := NewCounter(Default(), 1000)
	c.Add("hello world")
	if c.Used() == 0 {
		t.Error("expected non-zero usage after Add")
	}
	first := c.Used()
	c.Add(strings.Repeat("more text ", 10))
	if c.Used() <= first {
		t.Error("expected usage to increase after second Add")
	}
}

func TestCounter_AddCount(t *testing.T) {
	c := NewCounter(Default(), 1000)
	c.AddCount(42)
	if c.Used() != 42 {
		t.Errorf("AddCount: want 42 got %d", c.Used())
	}
	c.AddCount(8)
	if c.Used() != 50 {
		t.Errorf("AddCount cumulative: want 50 got %d", c.Used())
	}
}

func TestCounter_Reset(t *testing.T) {
	c := NewCounter(Default(), 1000)
	c.AddCount(100)
	c.Reset()
	if c.Used() != 0 {
		t.Errorf("Reset: want 0 got %d", c.Used())
	}
}

func TestCounter_Percent_Caps(t *testing.T) {
	c := NewCounter(Default(), 1000)
	c.AddCount(500)
	if p := c.Percent(); p != 0.5 {
		t.Errorf("Percent want 0.5 got %v", p)
	}
	c.AddCount(10_000) // overflow
	if p := c.Percent(); p != 1.0 {
		t.Errorf("Percent overflow should cap at 1.0, got %v", p)
	}
}

func TestCounter_NilSafe(t *testing.T) {
	var c *Counter
	c.Add("anything")    // should not panic
	c.AddCount(5)        // should not panic
	c.Reset()
	if c.Used() != 0 || c.Max() != 0 || c.Percent() != 0 {
		t.Error("nil Counter should report zeros without panicking")
	}
}

func TestRegistry_LazyCreate(t *testing.T) {
	r := NewRegistry()
	c1 := r.Get("conv1", "qwen3-coder")
	if c1 == nil {
		t.Fatal("expected counter for conv1")
	}
	c2 := r.Get("conv1", "qwen3-coder")
	if c2 != c1 {
		t.Error("expected same counter instance on second Get")
	}
	c3 := r.Get("conv2", "claude-opus-4-7")
	if c3 == c1 {
		t.Error("different conv id should yield different counter")
	}
}

func TestRegistry_Drop(t *testing.T) {
	r := NewRegistry()
	c1 := r.Get("conv1", "qwen3-coder")
	c1.AddCount(100)
	r.Drop("conv1")
	c2 := r.Get("conv1", "qwen3-coder")
	if c2 == c1 {
		t.Error("expected new counter after Drop")
	}
	if c2.Used() != 0 {
		t.Errorf("dropped counter recreated should be empty, got %d", c2.Used())
	}
}
