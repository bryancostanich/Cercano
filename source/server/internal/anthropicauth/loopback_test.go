package anthropicauth

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestCallbackErrorPageEscapesReflectedError drives the real loopback
// /callback handler with a <script> payload in the untrusted "error" query
// parameter and asserts the served HTML escapes it rather than reflecting it
// verbatim. The error branch runs before the state check, so this path is
// reachable by any local process that finds the ephemeral port during sign-in.
func TestCallbackErrorPageEscapesReflectedError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, err := (Flow{}).Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait serves the loopback listener and returns once /callback is hit.
	done := make(chan error, 1)
	go func() {
		_, werr := p.Wait(ctx)
		done <- werr
	}()

	payload := `<script>alert(1)</script>`
	resp, err := http.Get(p.redirectURI + "?error=" + url.QueryEscape(payload))
	if err != nil {
		t.Fatalf("GET /callback: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	// Wait should return the authorize-error (error branch taken).
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return after callback")
	}

	page := string(body)
	if strings.Contains(page, payload) {
		t.Errorf("served page reflects unescaped error payload (XSS):\n%s", page)
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Errorf("expected the error payload to be HTML-escaped in the page, got:\n%s", page)
	}
}
