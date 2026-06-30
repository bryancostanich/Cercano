package tokens

import "testing"

func TestEstimate(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"empty", "", 0},
		{"four chars", "abcd", 1},
		{"eight chars", "abcdefgh", 2},
		{"short sentence", "Hello, world!", 3},
		{"not divisible", "abc", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Estimate(tt.content)
			if got != tt.want {
				t.Errorf("Estimate(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}
