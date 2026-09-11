package responses

import (
	"cercano/source/server/internal/llm"
	"net/http"
	"strings"
	"testing"
)

func TestAuthenticationDiagnosticsDoNotPrintProviderBodies(t *testing.T) {
	for _, status := range []int{401, 403} {
		client := NewClient(Config{})
		err := client.normalizeHTTP(&http.Response{StatusCode: status, Header: http.Header{}}, []byte(`{"error":{"message":"secret-key-reflected-by-provider"}}`))
		if strings.Contains(err.Error(), "secret-key") {
			t.Fatalf("authentication error leaked provider body: %v", err)
		}
	}
}

func TestAuthenticationStatusTakesPriorityOverErrorProse(t *testing.T) {
	err := NewClient(Config{}).normalizeHTTP(&http.Response{StatusCode: 401, Header: http.Header{}}, []byte(`{"error":{"message":"Your input exceeds the context window of this model."}}`))
	if llm.ClassOf(err) != llm.ErrAuth {
		t.Fatalf("authentication bypassed by prose: %v", err)
	}
}
