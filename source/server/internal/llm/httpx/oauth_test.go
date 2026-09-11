package httpx

import (
	"cercano/source/server/internal/llm"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOAuthFailureRetainsOnlyAllowlistedCode(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`{"error":"invalid_grant","error_description":"secret-refresh-token"}`, "invalid_grant"},
		{`{"error":"secret-refresh-token"}`, "unrecognized"},
		{`{"error":{"message":"secret-refresh-token"}}`, "unrecognized"},
		{"secret-refresh-token", "unrecognized"},
		{strings.Repeat(" ", 17<<10) + `{"error":"invalid_grant"}`, "unrecognized"},
	} {
		response := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(tc.body))}
		err := OAuthFailure(response)
		var endpoint *llm.TokenEndpointError
		if !errors.As(err, &endpoint) || endpoint.Code != tc.code || strings.Contains(err.Error(), "secret-refresh-token") {
			t.Fatalf("unsafe or incorrect error: %v", err)
		}
	}
}
