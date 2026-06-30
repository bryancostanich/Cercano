package theme

import "testing"

func TestCrackerHasContentColors(t *testing.T) {
	p := Cracker()
	// hexOf is added in Task 3; for now assert non-nil via String().
	for name, c := range map[string]interface{}{
		"BufferLink": p.BufferLink, "BufferCode": p.BufferCode, "BufferLime": p.BufferLime,
		"BufferError": p.BufferError, "BufferUserBg": p.BufferUserBg,
	} {
		if c == nil {
			t.Fatalf("Cracker().%s is nil", name)
		}
	}
}
