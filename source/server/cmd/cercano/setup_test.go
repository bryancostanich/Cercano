package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFilterChatModels_ExcludesEmbeddings(t *testing.T) {
	installed := []string{
		"qwen3-coder:latest",
		"nomic-embed-text:latest",
		"gemma4:26b",
		"mxbai-embed-large",
		"phi4:14b",
	}
	got := filterChatModels(installed)
	for _, m := range got {
		family := strings.TrimSuffix(strings.SplitN(m, ":", 2)[0], ":latest")
		if embeddingModelNames[family] {
			t.Errorf("embedding model %q leaked into chat list", m)
		}
	}
	// Three chat models expected.
	if len(got) != 3 {
		t.Errorf("expected 3 chat models, got %d: %v", len(got), got)
	}
}

func TestFilterChatModels_Empty(t *testing.T) {
	got := filterChatModels([]string{"nomic-embed-text"})
	if len(got) != 0 {
		t.Errorf("expected empty slice when only embeddings installed, got %v", got)
	}
}

func TestPickCuratedModel_ValidChoice(t *testing.T) {
	in := bytes.NewBufferString("1\n")
	out := &bytes.Buffer{}
	picked, err := pickCuratedModel(in, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if picked != curatedChatModels[0].Name {
		t.Errorf("expected %q, got %q", curatedChatModels[0].Name, picked)
	}
	// The curated list must have rendered to output so users can see options.
	if !strings.Contains(out.String(), curatedChatModels[0].Name) {
		t.Error("curated list not shown to user")
	}
	if !strings.Contains(out.String(), curatedChatModels[0].Description) {
		t.Error("descriptions not shown to user")
	}
}

func TestPickCuratedModel_Skip(t *testing.T) {
	in := bytes.NewBufferString("0\n")
	out := &bytes.Buffer{}
	picked, err := pickCuratedModel(in, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if picked != "" {
		t.Errorf("expected empty selection on 0, got %q", picked)
	}
}

func TestPickCuratedModel_InvalidChoice(t *testing.T) {
	cases := []string{"\n", "99\n", "abc\n", "-1\n"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			in := bytes.NewBufferString(c)
			out := &bytes.Buffer{}
			if _, err := pickCuratedModel(in, out); err == nil {
				t.Errorf("expected error for input %q", c)
			}
		})
	}
}

func TestPromptYesNo(t *testing.T) {
	cases := []struct {
		input      string
		defaultYes bool
		want       bool
	}{
		{"y\n", false, true},
		{"yes\n", false, true},
		{"Y\n", false, true},
		{"n\n", true, false},
		{"no\n", true, false},
		{"\n", true, true},   // empty → default
		{"\n", false, false}, // empty → default
		{"xyz\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.input+"_"+func() string {
			if tc.defaultYes {
				return "defaultY"
			}
			return "defaultN"
		}(), func(t *testing.T) {
			out := &bytes.Buffer{}
			in := bytes.NewBufferString(tc.input)
			got := promptYesNo(out, in, "? ", tc.defaultYes)
			if got != tc.want {
				t.Errorf("input=%q default=%v: got %v, want %v", tc.input, tc.defaultYes, got, tc.want)
			}
		})
	}
}
