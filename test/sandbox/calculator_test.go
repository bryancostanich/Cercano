package sandbox

import (
	"testing"
)

func TestAdd(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{
			name:     "positive numbers",
			a:        5,
			b:        3,
			expected: 8,
		},
		{
			name:     "negative numbers",
			a:        -5,
			b:        -3,
			expected: -8,
		},
		{
			name:     "mixed signs",
			a:        5,
			b:        -3,
			expected: 2,
		},
		{
			name:     "zero and positive",
			a:        0,
			b:        5,
			expected: 5,
		},
		{
			name:     "zero and negative",
			a:        0,
			b:        -5,
			expected: -5,
		},
		{
			name:     "both zero",
			a:        0,
			b:        0,
			expected: 0,
		},
		{
			name:     "large numbers",
			a:        1000000,
			b:        2000000,
			expected: 3000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Add(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Add(%d, %d) = %d; expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestSubtract(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{
			name:     "positive numbers",
			a:        5,
			b:        3,
			expected: 2,
		},
		{
			name:     "negative numbers",
			a:        -5,
			b:        -3,
			expected: -2,
		},
		{
			name:     "mixed signs",
			a:        5,
			b:        -3,
			expected: 8,
		},
		{
			name:     "zero and positive",
			a:        0,
			b:        5,
			expected: -5,
		},
		{
			name:     "zero and negative",
			a:        0,
			b:        -5,
			expected: 5,
		},
		{
			name:     "both zero",
			a:        0,
			b:        0,
			expected: 0,
		},
		{
			name:     "large numbers",
			a:        2000000,
			b:        1000000,
			expected: 1000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Subtract(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Subtract(%d, %d) = %d; expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestMultiply(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{
			name:     "positive numbers",
			a:        5,
			b:        3,
			expected: 15,
		},
		{
			name:     "negative numbers",
			a:        -5,
			b:        -3,
			expected: 15,
		},
		{
			name:     "mixed signs",
			a:        5,
			b:        -3,
			expected: -15,
		},
		{
			name:     "zero and positive",
			a:        0,
			b:        5,
			expected: 0,
		},
		{
			name:     "zero and negative",
			a:        0,
			b:        -5,
			expected: 0,
		},
		{
			name:     "one and zero",
			a:        1,
			b:        0,
			expected: 0,
		},
		{
			name:     "one and one",
			a:        1,
			b:        1,
			expected: 1,
		},
		{
			name:     "large numbers",
			a:        1000,
			b:        1000,
			expected: 1000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Multiply(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Multiply(%d, %d) = %d; expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestDivide(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		b        int
		expected int
		hasError bool
	}{
		{
			name:     "positive numbers",
			a:        10,
			b:        2,
			expected: 5,
			hasError: false,
		},
		{
			name:     "negative numbers",
			a:        -10,
			b:        -2,
			expected: 5,
			hasError: false,
		},
		{
			name:     "mixed signs",
			a:        10,
			b:        -2,
			expected: -5,
			hasError: false,
		},
		{
			name:     "zero dividend",
			a:        0,
			b:        5,
			expected: 0,
			hasError: false,
		},
		{
			name:     "zero divisor",
			a:        10,
			b:        0,
			expected: 0,
			hasError: true,
		},
		{
			name:     "division with remainder",
			a:        7,
			b:        3,
			expected: 2,
			hasError: false,
		},
		{
			name:     "negative dividend, positive divisor",
			a:        -7,
			b:        3,
			expected: -2,
			hasError: false,
		},
		{
			name:     "positive dividend, negative divisor",
			a:        7,
			b:        -3,
			expected: -2,
			hasError: false,
		},
		{
			name:     "large numbers",
			a:        1000000,
			b:        1000,
			expected: 1000,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Divide(tt.a, tt.b)
			if tt.hasError {
				if err == nil {
					t.Errorf("Divide(%d, %d) should have returned an error", tt.a, tt.b)
				}
			} else {
				if err != nil {
					t.Errorf("Divide(%d, %d) returned unexpected error: %v", tt.a, tt.b, err)
				}
				if result != tt.expected {
					t.Errorf("Divide(%d, %d) = %d; expected %d", tt.a, tt.b, result, tt.expected)
				}
			}
		})
	}
}