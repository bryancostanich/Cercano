package modelpolicy_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/internal/llm/bedrock"
	ollama "cercano/source/server/internal/llm/ollama"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/llm/responses"
	"cercano/source/server/internal/modelpolicy"
)

func TestRealAdaptersCheckActualModelBeforeSending(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	tests := []struct {
		name, provider, placement, suffix, response string
		build                                       func(string) (inference.Provider, error)
	}{
		{"openai", "openai", "external", "/v1", `{"id":"x","object":"chat.completion","model":"approved","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, func(base string) (inference.Provider, error) {
			return openai.NewClient(openai.Config{BaseURL: base + "/v1", APIKey: "test"}), nil
		}},
		{"anthropic", "anthropic", "external", "", `{"id":"x","type":"message","role":"assistant","model":"approved","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, func(base string) (inference.Provider, error) {
			return anthropic.NewClient(anthropic.Config{BaseURL: base, APIKey: "test"}), nil
		}},
		{"responses", "openai", "external", "/v1", `{"id":"x","object":"response","status":"completed","model":"approved","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`, func(base string) (inference.Provider, error) {
			return responses.NewClient(responses.Config{BaseURL: base + "/v1", APIKey: "test"}), nil
		}},
		{"ollama", "ollama", "local", "", `{"model":"approved","message":{"role":"assistant","content":"ok"},"done":true}`, func(base string) (inference.Provider, error) {
			return ollama.NewClient(ollama.Config{BaseURL: base}), nil
		}},
		{"bedrock", "bedrock", "external", "", `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"metrics":{"latencyMs":1}}`, func(base string) (inference.Provider, error) {
			return bedrock.NewClient(bedrock.Config{BaseURL: base, Region: "us-east-1"})
		}},
		{"local-compatible", "llama_server", "local", "/v1", `{"model":"approved","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, func(base string) (inference.Provider, error) {
			return openai.NewClient(openai.Config{BaseURL: base + "/v1", PolicyProvider: "llama_server", PolicyPlacement: "local"}), nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintln(w, tt.response)
			}))
			defer server.Close()
			var checked atomic.Int32
			ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
				checked.Add(1)
				if a.Endpoint != server.URL+tt.suffix || a.Provider != tt.provider || a.Placement != tt.placement {
					t.Errorf("wrong physical identity: %+v", a)
				}
				if a.Model != "approved" {
					return modelpolicy.Deny(a, "not approved")
				}
				return nil
			}))
			provider, err := tt.build(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := llm.ChatRequest{Model: "approved", MaxTokens: 8, Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}}}}
			if _, err = provider.Chat(ctx, req); err != nil {
				t.Fatal("allowed request failed:", err)
			}
			req.Model = "forbidden"
			_, err = provider.Chat(ctx, req)
			if llm.ClassOf(err) != llm.ErrPermission {
				t.Fatalf("nonterminal denial: %v class=%s", err, llm.ClassOf(err))
			}
			stream, err := provider.StreamChat(ctx, req)
			if stream != nil {
				// Some adapters open the stream lazily and report dial errors on Next.
				for err == nil {
					_, ok, nextErr := stream.Next()
					err = nextErr
					if !ok {
						break
					}
				}
				stream.Close()
			}
			if llm.ClassOf(err) != llm.ErrPermission {
				t.Fatalf("stream denial: %v class=%s", err, llm.ClassOf(err))
			}
			if hits.Load() != 1 || checked.Load() != 3 {
				t.Fatalf("hits=%d checks=%d", hits.Load(), checked.Load())
			}
		})
	}
}

func TestResilienceRechecksRestrictionBeforeRetryAndFallback(t *testing.T) {
	for _, mode := range []string{"retry-revoked", "fallback-forbidden"} {
		t.Run(mode, func(t *testing.T) {
			var hits, checks atomic.Int32
			var revoked atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				revoked.Store(true)
				w.Header().Set("Content-Type", "application/json")
				status := 503
				if mode == "fallback-forbidden" {
					status = 429
				}
				w.WriteHeader(status)
				if status == 429 {
					fmt.Fprint(w, `{"error":{"message":"quota exhausted","type":"insufficient_quota","code":"insufficient_quota"}}`)
				} else {
					fmt.Fprint(w, `{"error":{"message":"busy"}}`)
				}
			}))
			defer server.Close()
			primary := openai.NewClient(openai.Config{BaseURL: server.URL + "/v1", APIKey: "test"})
			backup := openai.NewClient(openai.Config{BaseURL: server.URL + "/other", APIKey: "test"})
			ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
				checks.Add(1)
				if strings.HasSuffix(a.Endpoint, "/other") || revoked.Load() {
					return modelpolicy.Deny(a, "revoked")
				}
				return nil
			}))
			provider := resilience.New(primary, resilience.Options{Backup: backup, RetryWait: time.Millisecond, RetryWaitCap: time.Millisecond})
			_, err := provider.Chat(ctx, llm.ChatRequest{Model: "approved"})
			if llm.ClassOf(err) != llm.ErrPermission || hits.Load() != 1 || checks.Load() < 2 {
				t.Fatalf("restriction bypass: %v hits=%d checks=%d", err, hits.Load(), checks.Load())
			}
		})
	}
}
