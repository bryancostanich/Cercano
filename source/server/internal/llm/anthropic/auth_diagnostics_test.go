package anthropic

import (
	"strings"
	"testing"
)

func TestAuthenticationDiagnosticsDoNotPrintProviderBodies(t *testing.T) {
	for _, status := range []int{401, 403} {
		client, _ := fixture(t, status, nil, `{"type":"error","error":{"type":"authentication_error","message":"secret-key-reflected-by-provider"}}`)
		err := chatErr(t, client)
		if strings.Contains(err.Error(), "secret-key") {
			t.Fatalf("authentication error leaked provider body: %v", err)
		}
	}
}
