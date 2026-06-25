package slash

import "testing"

func TestLocusValidatesMode(t *testing.T) {
	if validLocusMode("cloud_primary") != true {
		t.Error("cloud_primary should be valid")
	}
	if validLocusMode("nope") != false {
		t.Error("nope should be invalid")
	}
}
