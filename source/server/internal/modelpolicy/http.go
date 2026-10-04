package modelpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Protocol identifies the wire format, independently of the provider identity.
// Local llama-server and mistral.rs use OpenAI's format but remain local routes.
type Protocol string

const (
	OpenAI    Protocol = "openai"
	Anthropic Protocol = "anthropic"
	Responses Protocol = "responses"
	Ollama    Protocol = "ollama"
	Bedrock   Protocol = "bedrock"
)

// ModelResolver resolves a supervised runtime's physical model when its wire
// protocol uses an alias such as "default". It runs again before every send.
type ModelResolver func(context.Context, string, string) (string, error)

type transport struct {
	resolve             ModelResolver
	next                http.RoundTripper
	provider, placement string
	protocol            Protocol
}

// Client copies a client and wraps its transport, preserving timeouts and TLS
// settings. Even hidden SDK retries cross this boundary again. Standalone calls
// retain the original redirect behavior and do not parse request bodies.
func Client(base *http.Client, provider, placement string, protocol Protocol, resolve ...ModelResolver) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	c := *base
	next := c.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	gate := &transport{next: next, provider: provider, placement: placement, protocol: protocol}
	if len(resolve) > 0 {
		gate.resolve = resolve[0]
	}
	c.Transport = gate
	redirect := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if Managed(req.Context()) {
			return Deny(Attempt{Provider: provider, Placement: placement}, "model endpoint redirected")
		}
		if redirect != nil {
			return redirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &c
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if Managed(req.Context()) && req.Method != "GET" && req.Method != "HEAD" {
		a, err := t.attempt(req)
		if err != nil {
			return nil, err
		}
		if err = Check(req.Context(), a); err != nil {
			return nil, err
		}
	}
	return t.next.RoundTrip(req)
}

func (t *transport) attempt(req *http.Request) (Attempt, error) {
	a := Attempt{Provider: t.provider, Placement: t.placement}
	u := *req.URL
	// URLs with userinfo or query data cannot be canonical policy endpoints.
	// Do not include such values in the error presented to the user or logs.
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		return a, Deny(a, "ambiguous model endpoint")
	}
	path := u.EscapedPath()
	suffixes := map[Protocol][]string{
		OpenAI:    {"/chat/completions", "/completions", "/embeddings"},
		Anthropic: {"/v1/messages"}, Responses: {"/responses"},
		Ollama: {"/api/chat", "/api/generate", "/api/embed", "/api/embeddings"},
	}
	matched := false
	if t.protocol == Bedrock {
		for _, suffix := range []string{"/converse", "/converse-stream", "/invoke", "/invoke-with-response-stream"} {
			if !strings.HasSuffix(path, suffix) {
				continue
			}
			prefix, model, ok := strings.Cut(strings.TrimSuffix(path, suffix), "/model/")
			if !ok {
				break
			}
			decoded, err := url.PathUnescape(model)
			if err != nil || decoded == "" {
				break
			}
			path = prefix
			a.Model = decoded
			matched = true
			break
		}
	} else {
		for _, suffix := range suffixes[t.protocol] {
			if strings.HasSuffix(path, suffix) {
				path = strings.TrimSuffix(path, suffix)
				matched = true
				break
			}
		}
	}
	if !matched || strings.Contains(path, "%") {
		return a, Deny(a, "unrecognized model endpoint")
	}
	u.Path = path
	u.RawPath = ""
	a.Endpoint = strings.TrimRight(u.String(), "/")
	if t.protocol != Bedrock {
		if req.GetBody == nil {
			return a, Deny(a, "model identity cannot be verified")
		}
		body, err := req.GetBody()
		if err != nil {
			return a, Deny(a, "model identity cannot be verified")
		}
		defer body.Close()
		// Only the model field is retained. Request content never leaves this process
		// or enters the authority callback. Bound the additional decoding allocation.
		var wire struct {
			Model string `json:"model"`
		}
		d := json.NewDecoder(io.LimitReader(body, 128<<20))
		if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || strings.TrimSpace(wire.Model) == "" {
			return a, Deny(a, "model identity cannot be verified")
		}
		a.Model = wire.Model
	}
	if t.resolve != nil {
		actual, err := t.resolve(req.Context(), a.Endpoint, a.Model)
		if err != nil || actual == "" {
			return a, Deny(a, "running model identity cannot be verified")
		}
		a.Model = actual
	}
	return a, nil
}

// Placement classifies a literal loopback endpoint; remote Ollama installations
// remain external destinations even though they speak a local runtime protocol.
func Placement(u *url.URL) string {
	if u != nil {
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			return "local"
		}
	}
	return "external"
}
