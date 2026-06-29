package form

import "testing"

func TestButtonFieldActivates(t *testing.T) {
	f := NewButton("theme-save", "Save", true)
	_, committed, val := f.Update(enter())
	if !committed || val != "activate" {
		t.Fatalf("enabled button enter = (%v,%q), want (true,activate)", committed, val)
	}
	if f.Editing() {
		t.Fatal("button never enters editing")
	}
}

func TestButtonFieldDisabledInert(t *testing.T) {
	f := NewButton("theme-delete", "Delete", false)
	_, committed, _ := f.Update(enter())
	if committed {
		t.Fatal("disabled button must not commit")
	}
}
