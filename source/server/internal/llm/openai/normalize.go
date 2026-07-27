package openai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// normalize maps go-openai vendor errors and transport failures into the
// provider-agnostic llm.Error taxonomy. Context cancellation passes through
// untouched. go-openai does not expose response headers on its typed errors,
// so RetryAfter is never available on this flavor; quota detection relies on
// OpenAI's explicit "insufficient_quota" code instead.
func (c *Client) normalize(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ae *goopenai.APIError
	if errors.As(err, &ae) {
		return &llm.Error{
			Class:      c.classify(ae.HTTPStatusCode, codeString(ae.Code), ae.Type, ae.Message),
			Provider:   c.Name(),
			StatusCode: ae.HTTPStatusCode,
			Err:        err,
		}
	}
	var re *goopenai.RequestError
	if errors.As(err, &re) {
		return &llm.Error{
			Class:      c.classify(re.HTTPStatusCode, "", "", re.Error()),
			Provider:   c.Name(),
			StatusCode: re.HTTPStatusCode,
			Err:        err,
		}
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return &llm.Error{Class: llm.ErrNetwork, Provider: c.Name(), Err: err}
	}
	return &llm.Error{Class: llm.ErrUnknown, Provider: c.Name(), Err: err}
}

func (c *Client) classify(status int, code, typ, msg string) llm.ErrorClass {
	if c.backend == "mistralrs" && isMistralRSContextTooLong(status, msg) {
		return llm.ErrInvalidRequest
	}
	return classifyOpenAI(status, code, typ, msg)
}

func isMistralRSContextTooLong(status int, msg string) bool {
	if status < 500 {
		return false
	}
	m := strings.ToLower(msg)
	return strings.Contains(m, "no response received from the model") ||
		(strings.Contains(m, "sequence") && strings.Contains(m, "too long") && strings.Contains(m, "kv cache"))
}

// classifyOpenAI is the OpenAI-dialect status/code → class mapping, shared by
// the chat_completions flavor here and reproduced in spirit by the responses
// flavor (which parses its own wire shape).
func classifyOpenAI(status int, code, typ, msg string) llm.ErrorClass {
	quotaMarked := code == "insufficient_quota" || typ == "insufficient_quota" ||
		strings.Contains(strings.ToLower(msg), "quota")
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return llm.ErrAuth
	case status == http.StatusTooManyRequests:
		if quotaMarked {
			return llm.ErrQuota
		}
		return llm.ErrBusy
	case status >= 500:
		return llm.ErrBusy
	case status >= 400:
		return llm.ErrInvalidRequest
	default:
		return llm.ErrUnknown
	}
}

// codeString flattens go-openai's any-typed error code (string or number).
func codeString(code any) string {
	if s, ok := code.(string); ok {
		return s
	}
	return ""
}
