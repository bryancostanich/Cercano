package policy

import (
	"bytes"
	"testing"
)

func TestStrictJSONRejectsCaseAliases(t *testing.T) {
	var out struct {
		Consent bool `json:"consent_recorded"`
	}
	for _, input := range []string{`{"CONSENT_RECORDED":true}`, `{"consent_recorded":false,"CONSENT_RECORDED":true}`} {
		if err := strictDecodeJSON([]byte(input), &out); err == nil {
			t.Fatalf("case alias accepted: %s", input)
		}
	}
}
func TestStrictJSONRejectsInvalidUTF8(t *testing.T) {
	var out struct {
		ID string `json:"install_id"`
	}
	input := append([]byte(`{"install_id":"`), 0xff)
	input = append(input, []byte(`"}`)...)
	if err := strictDecodeJSON(input, &out); err == nil {
		t.Fatal("invalid UTF8 silently replaced")
	}
}
func TestStrictJSONRejectsOversizedRecord(t *testing.T) {
	var out struct {
		ID string `json:"install_id"`
	}
	input := append([]byte(`{"install_id":"`), bytes.Repeat([]byte("x"), 1024*1024)...)
	input = append(input, []byte(`"}`)...)
	if err := strictDecodeJSON(input, &out); err == nil {
		t.Fatal("oversized record accepted")
	}
}
