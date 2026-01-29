package sandbox

import (
	"errors"
	"testing"
)

func TestAdd(t *testing.T) {
	tests := []struct {
		name string
		a    int
		b    int
		want int
	}{
		{
			name: "positive numbers",
			a:    5,
			b:    3,
			want: 8,
		},
		{
			name: "negative numbers",
			a:    -5,
			b:    -3,
			want: -8,
		},
		{
			name: "mixed signs",
			a:    5,
			b:    -3,
			want: 2,
		},
		{
			name: "zero",
			a:    0,
			b:    5,
			want: 5,
		},
		{
			name: "both zero",
			a:    0,
			b:    0,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Add(tt.a, tt.b); got != tt.want {
				t.Errorf("Add() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubtract(t *testing.T) {
	tests := []struct {
		name string
		a    int
		b    int
		want int
	}{
		{
			name: "positive numbers",
			a:    5,
			b:    3,
			want: 2,
		},
		{
			name: "negative numbers",
			a:    -5,
			b:    -3,
			want: -2,
		},
		{
			name: "mixed signs",
			a:    5,
			b:    -3,
			want: 8,
		},
		{
			name: "zero",
			a:    0,
			b:    5,
			want: -5,
		},
		{
			name: "both zero",
			a:    0,
			b:    0,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Subtract(tt.a, tt.b); got != tt.want {
				t.Errorf("Subtract() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMultiply(t *testing.T) {
	tests := []struct {
		name string
		a    int
		b    int
		want int
	}{
		{
			name: "positive numbers",
			a:    5,
			b:    3,
			want: 15,
		},
		{
			name: "negative numbers",
			a:    -5,
			b:    -3,
			want: 15,
		},
		{
			name: "mixed signs",
			a:    5,
			b:    -3,
			want: -15,
		},
		{
			name: "zero",
			a:    0,
			b:    5,
			want: 0,
		},
		{
			name: "one",
			a:    1,
			b:    5,
			want: 5,
		},
		{
			name: "negative one",
			a:    -1,
			b:    5,
			want: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Multiply(tt.a, tt.b); got != tt.want {
				t.Errorf("Multiply() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDivide(t *testing.T) {
	tests := []struct {
		name    string
		a       int
		b       int
		want    int
		wantErr bool
	}{
		{
			name:    "positive numbers",
			a:       10,
			b:       2,
			want:    5,
			wantErr: false,
		},
		{
			name:    "negative numbers",
			a:       -10,
			b:       -2,
			want:    5,
			wantErr: false,
		},
		{
			name:    "mixed signs",
			a:       10,
			b:       -2,
			want:    -5,
			wantErr: false,
		},
		{
			name:    "zero dividend",
			a:       0,
			b:       5,
			want:    0,
			wantErr: false,
		},
		{
			name:    "division by zero",
			a:       10,
			b:       0,
			want:    0,
			wantErr: true,
		},
		{
			name:    "zero divisor",
			a:       0,
			b:       0,
			want:    0,
			wantErr: true,
		},
		{
			name:    "negative divisor",
			a:       10,
			b:       -2,
			want:    -5,
			wantErr: false,
		},
		{
			name:    "large numbers",
			a:       1000000,
			b:       1000,
			want:    1000,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Divide(tt.a, tt.b)
			if (err != nil) != tt.wantErr {
				t.Errorf("Divide() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Divide() = %v, want %v", got, tt.want)
			}
		})
	}
}