package sandbox

import "errors"

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}

// Subtract returns the difference between a and b.
func Subtract(a, b int) int {
	return a - b
}

// Multiply returns the product of a and b.
func Multiply(a, b int) int {
	return a * b
}

// Divide returns the quotient of a divided by b.
// It returns an error if b is zero.
func Divide(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("cannot divide by zero")
	}
	return a / b, nil
}
