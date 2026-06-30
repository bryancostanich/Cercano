package ui

import "testing"

// clipboardImage must be safe to call and must never panic. On a machine with no
// image on the clipboard (CI, or non-macOS), it returns ok=false. This test just
// pins the contract; a real macOS smoke check is manual (see the spike note).
func TestClipboardImageContract(t *testing.T) {
	data, mt, ok := clipboardImage()
	if ok {
		if len(data) == 0 || mt == "" {
			t.Fatalf("ok=true must carry data + media type, got len=%d mt=%q", len(data), mt)
		}
	} else {
		if data != nil || mt != "" {
			t.Fatalf("ok=false must return zero values, got len=%d mt=%q", len(data), mt)
		}
	}
}
