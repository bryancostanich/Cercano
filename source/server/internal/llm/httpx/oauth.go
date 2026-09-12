package httpx

import (
	"encoding/json"
	"io"
	"net/http"

	"cercano/source/server/internal/llm"
)

// OAuthFailure reads a bounded response and retains only recognized OAuth
// codes. Arbitrary error strings and descriptions are never returned or logged.
func OAuthFailure(response *http.Response) error {
	var wire struct {
		Error string `json:"error"`
	}
	code := "unrecognized"
	if json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&wire) == nil {
		switch wire.Error {
		case "invalid_grant", "invalid_client", "invalid_request", "unauthorized_client", "unsupported_grant_type", "invalid_scope", "temporarily_unavailable", "server_error", "access_denied":
			code = wire.Error
		}
	}
	return &llm.TokenEndpointError{StatusCode: response.StatusCode, Code: code}
}
