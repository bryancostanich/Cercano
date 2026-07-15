package anthropicauth

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"time"
)

// Pending is a started authorization awaiting user approval. The caller shows
// AuthorizeURL (or opens it in a browser), then calls Wait to block for the
// loopback redirect — or Redeem with a manually pasted code for the
// headless/SSH fallback. Exactly one of Wait/Redeem should be called; both
// close the loopback listener.
type Pending struct {
	// AuthorizeURL is the URL to open/display for the user to approve.
	AuthorizeURL string

	state       string
	verifier    string
	redirectURI string
	listener    net.Listener
	flow        Flow
}

// Start binds a loopback listener on an ephemeral port, generates the PKCE
// pair and CSRF state, and builds the authorize URL pointing back at that
// port. The listener is held open until Wait/Redeem/Close.
func (f Flow) Start(ctx context.Context) (*Pending, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("anthropic oauth: bind loopback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// Match Claude Code's redirect exactly: host "localhost", path "/callback".
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	pk, err := newPKCE()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	state, err := randomToken()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return &Pending{
		AuthorizeURL: f.authorizeURL(redirectURI, pk.challenge, state),
		state:        state,
		verifier:     pk.verifier,
		redirectURI:  redirectURI,
		listener:     ln,
		flow:         f,
	}, nil
}

type callbackResult struct {
	code string
	err  error
}

// Wait serves the loopback listener until the browser hits /callback, renders
// a success (or failure) page to that browser, then exchanges the returned
// code for a token set. It returns ctx.Err() if the context is canceled
// first. The listener is always closed on return.
//
// Teardown is graceful: the handler flushes its response with Connection:
// close, and Wait shuts the server down with Shutdown (not Close) so the
// browser fully receives the page instead of a reset connection — the reset
// is what renders as "localhost refused to connect".
func (p *Pending) Wait(ctx context.Context) (*TokenSet, error) {
	done := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("error") != "":
			e := q.Get("error")
			writeBrowserPage(w, false, "Sign-in failed", "Anthropic returned an error: "+e+". You can close this window and try again in Cercano.")
			done <- callbackResult{err: fmt.Errorf("anthropic oauth: authorize error: %s", e)}
		case q.Get("code") == "":
			writeBrowserPage(w, false, "Sign-in failed", "No authorization code was returned. You can close this window and try again in Cercano.")
			done <- callbackResult{err: fmt.Errorf("anthropic oauth: callback missing code")}
		case q.Get("state") != p.state:
			writeBrowserPage(w, false, "Sign-in failed", "The sign-in could not be verified (state mismatch). You can close this window and try again in Cercano.")
			done <- callbackResult{err: fmt.Errorf("anthropic oauth: state mismatch")}
		default:
			writeBrowserPage(w, true, "Successfully authenticated", "You're signed in to Claude. You may close this window or tab and return to Cercano.")
			done <- callbackResult{code: q.Get("code")}
		}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(p.listener) }()

	var cb callbackResult
	select {
	case <-ctx.Done():
		_ = srv.Close()
		return nil, ctx.Err()
	case cb = <-done:
	}

	// Drain the response gracefully before returning; the handler already set
	// Connection: close, so the browser drops its socket and Shutdown returns
	// promptly rather than blocking on a keep-alive connection.
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)

	if cb.err != nil {
		return nil, cb.err
	}
	return p.flow.exchange(ctx, cb.code, p.state, p.verifier, p.redirectURI)
}

// Redeem completes the flow from a manually pasted code — the code=true
// fallback for headless/SSH sessions where the browser can't reach the
// loopback port. The pasted value may arrive as "<code>#<state>"; when it
// does the embedded state is used, otherwise the state we generated. The
// listener is closed on return.
func (p *Pending) Redeem(ctx context.Context, pasted string) (*TokenSet, error) {
	defer p.listener.Close()
	code, state := strings.TrimSpace(pasted), p.state
	if i := strings.IndexByte(code, '#'); i >= 0 {
		code, state = code[:i], code[i+1:]
	}
	if code == "" {
		return nil, fmt.Errorf("anthropic oauth: empty pasted code")
	}
	return p.flow.exchange(ctx, code, state, p.verifier, p.redirectURI)
}

// Close releases the loopback listener without completing the flow (e.g. the
// user canceled). Safe to call more than once.
func (p *Pending) Close() error {
	return p.listener.Close()
}

// writeBrowserPage renders the page shown in the user's browser after the
// redirect, then flushes it so the bytes reach the browser before the server
// is torn down. Connection: close tells the browser to drop the socket, which
// keeps graceful shutdown from racing a lingering keep-alive connection.
//
// title and msg are plain text and are HTML-escaped before interpolation: the
// failure page reflects the untrusted "error" query parameter into msg, and
// that branch runs before the state check, so an unescaped value would let any
// local process that finds the loopback port inject markup into this page.
func writeBrowserPage(w http.ResponseWriter, success bool, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Connection", "close")
	accent := "#BDF000" // lime — success
	if !success {
		accent = "#EA5555" // red — failure
	}
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><meta charset=utf-8>"+
		"<title>Cercano — %s</title></head>"+
		"<body style=\"font-family:system-ui,-apple-system,sans-serif;background:#1A1A1A;"+
		"color:#EA8212;display:flex;flex-direction:column;align-items:center;"+
		"justify-content:center;height:100vh;margin:0;text-align:center;padding:0 1.5rem\">"+
		"<div style=\"font-size:1.5rem;color:%s;font-weight:600;margin-bottom:.6rem\">%s</div>"+
		"<div style=\"color:#C8C8C8;max-width:32rem;line-height:1.5\">%s</div></body></html>",
		html.EscapeString(title), accent, html.EscapeString(title), html.EscapeString(msg))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
