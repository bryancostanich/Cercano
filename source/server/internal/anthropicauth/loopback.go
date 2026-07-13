package anthropicauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
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

// Wait serves the loopback listener until the browser hits /callback, then
// exchanges the returned code for a token set. It returns ctx.Err() if the
// context is canceled first. The listener is always closed on return.
func (p *Pending) Wait(ctx context.Context) (*TokenSet, error) {
	defer p.listener.Close()

	type callback struct {
		code string
		err  error
	}
	done := make(chan callback, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writeBrowserPage(w, "Sign-in failed: "+e)
			done <- callback{err: fmt.Errorf("anthropic oauth: authorize error: %s", e)}
			return
		}
		code, gotState := q.Get("code"), q.Get("state")
		if code == "" {
			writeBrowserPage(w, "Sign-in failed: no authorization code returned.")
			done <- callback{err: fmt.Errorf("anthropic oauth: callback missing code")}
			return
		}
		if gotState != p.state {
			writeBrowserPage(w, "Sign-in failed: state mismatch.")
			done <- callback{err: fmt.Errorf("anthropic oauth: state mismatch")}
			return
		}
		writeBrowserPage(w, "Sign-in complete. You can close this window and return to Cercano.")
		done <- callback{code: code}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(p.listener) }()
	defer srv.Close()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case cb := <-done:
		if cb.err != nil {
			return nil, cb.err
		}
		return p.flow.exchange(ctx, cb.code, p.state, p.verifier, p.redirectURI)
	}
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

// writeBrowserPage renders a minimal HTML page shown in the user's browser
// after the redirect. Best-effort; a write error is not actionable here.
func writeBrowserPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>Cercano</title>"+
		"<body style=\"font-family:system-ui;background:#1A1A1A;color:#EA8212;"+
		"display:flex;align-items:center;justify-content:center;height:100vh;margin:0\">"+
		"<p>%s</p></body>", msg)
}
