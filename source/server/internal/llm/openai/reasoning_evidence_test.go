package openai

import (
	"testing"
)

func TestDiagnosticReasoningBooleanCollapsesEmptyStates(t *testing.T) {
	for _, delta := range []string{`{}`, `{"reasoning_content":null}`, `{"reasoning_content":""}`} {
		body := []byte("data: {\"choices\":[{\"index\":0,\"delta\":" + delta + ",\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		reason, _, _, err := parseDiagnosticSSE(body)
		if err != nil || reason != "" {
			t.Fatalf("unexpected baseline %q %v", reason, err)
		}
	}
}

func TestDiagnosticReasoningEvidence(t *testing.T) {
	for _, tc := range []struct {
		delta, state              string
		nulls, empty, text, bytes int
	}{
		{`{}`, "absent", 0, 0, 0, 0},
		{`{"reasoning_content":null}`, "null", 1, 0, 0, 0},
		{`{"reasoning_content":""}`, "empty", 0, 1, 0, 0},
		{`{"reasoning_content":"α"}`, "nonempty", 0, 0, 1, 2},
	} {
		body := []byte("data: {\"choices\":[{\"index\":0,\"delta\":" + tc.delta + ",\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		if _, _, _, err := parseDiagnosticSSE(body); err != nil {
			t.Fatal(err)
		}
		got := diagnosticReasoningEvidence(body)
		want := ReasoningEvidence{State: tc.state, NullChunks: tc.nulls, EmptyChunks: tc.empty, TextChunks: tc.text, Bytes: tc.bytes}
		if got != want {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
	body := []byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":null}}]}\n\ndata: {\"choices\":[{\"delta\":{\"reasoning_content\":\"\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"reasoning_content\":\"abc\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	got := diagnosticReasoningEvidence(body)
	if got != (ReasoningEvidence{State: "nonempty", NullChunks: 1, EmptyChunks: 1, TextChunks: 1, Bytes: 3}) {
		t.Fatalf("mixed %+v", got)
	}
}
