package responses

import (
	"cercano/source/server/internal/llm"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestUsagePresence(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		known bool
	}{
		{`{}`, false}, {`{"input_tokens":null,"output_tokens":null}`, false},
		{`{"input_tokens":0,"output_tokens":0}`, true},
		{`{"input_tokens":11,"output_tokens":7,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}`, true},
	} {
		var u usage
		if err := json.Unmarshal([]byte(tc.raw), &u); err != nil {
			t.Fatal(err)
		}
		out := u.normalized()
		if out.TotalsKnown() != tc.known {
			t.Fatalf("%s => %+v", tc.raw, out)
		}
		if out.CacheRead.Known && (out.Input.Value != 11 || out.Output.Value != 7 || out.Reasoning.Value != 2) {
			t.Fatalf("breakdown incorrectly added: %+v", out)
		}
	}
}

func TestFailedStreamRetainsReportedUsage(t *testing.T) {
	rdr := newStreamReader(io.NopCloser(strings.NewReader("data: "+`{"type":"response.failed","response":{"usage":{"input_tokens":11,"output_tokens":7},"error":{"message":"failed","type":"server_error"}}}`+"\n\n")), "fake")
	out, err := llm.CollectStream(t.Context(), rdr, nil, nil)
	if err == nil {
		t.Fatal("expected provider error")
	}
	if out.Usage.Input != llm.ReportedTokens(11) || out.Usage.Output != llm.ReportedTokens(7) {
		t.Fatalf("lost failure usage: %+v", out.Usage)
	}
}
