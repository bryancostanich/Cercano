package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestSDKRetriesAreSeparateAttempts(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.Header().Set("X-Amzn-Errortype", "ServiceUnavailableException")
			w.WriteHeader(503)
			w.Write([]byte(`{"message":"retry"}`))
			return
		}
		w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":11,"outputTokens":7,"totalTokens":18,"cacheReadInputTokens":3,"cacheWriteInputTokens":5},"metrics":{"latencyMs":1}}`))
	}))
	defer srv.Close()
	api := bedrockruntime.New(bedrockruntime.Options{Region: "us-east-1", BaseEndpoint: aws.String(srv.URL), Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "fake", SecretAccessKey: "fake"}, nil
	}), Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = 2
		o.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
	})})
	c := &Client{api: api, model: "fake"}
	var got []usage.AttemptObservation
	ctx := usage.WithAttempts(t.Context(), func(o usage.AttemptObservation) bool { got = append(got, o); return true }, usage.Attribution{Source: "main"})
	ctx = usage.WithAttemptProfile(ctx, "selected-profile", "secondary")
	out, err := c.Chat(ctx, llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	starts, ends := 0, 0
	ids := map[string]bool{}
	for _, o := range got {
		if o.Revision == 1 {
			starts++
			ids[o.ID] = true
		}
		if o.Outcome != usage.Started {
			if o.Profile != "selected-profile" || o.Destination != "secondary" {
				t.Fatalf("selected route overwritten: %+v", o)
			}
			ends++
			if o.Outcome == usage.Completed && o.Tokens.Input != llm.ReportedTokens(19) {
				t.Fatalf("cache total=%+v", o)
			}
		}
	}
	if calls.Load() != 2 || starts != 2 || ends != 2 || len(ids) != 2 {
		t.Fatalf("requests=%d starts=%d ends=%d ids=%d observations=%+v", calls.Load(), starts, ends, len(ids), got)
	}
	if out.Usage.Input != llm.ReportedTokens(19) || out.Usage.Output != llm.ReportedTokens(7) || out.InputTokens != 11 {
		t.Fatalf("usage=%+v", out)
	}
}

func TestBedrockUsagePresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   *types.TokenUsage
		known bool
		input int64
	}{
		{"absent", nil, false, 0},
		{"uncached zero", &types.TokenUsage{InputTokens: aws.Int32(0), OutputTokens: aws.Int32(0)}, true, 0},
		{"cache inclusive", &types.TokenUsage{InputTokens: aws.Int32(11), OutputTokens: aws.Int32(7), CacheReadInputTokens: aws.Int32(3), CacheWriteInputTokens: aws.Int32(5)}, true, 19},
		{"partial cache", &types.TokenUsage{InputTokens: aws.Int32(11), OutputTokens: aws.Int32(7), CacheReadInputTokens: aws.Int32(3)}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizedUsage(tc.raw)
			if got.TotalsKnown() != tc.known || got.Input.Value != tc.input {
				t.Fatalf("usage=%+v", got)
			}
			ev, _ := mapStreamEvent(&types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: tc.raw}})
			if ev.Usage != got {
				t.Fatalf("stream differs=%+v", ev.Usage)
			}
		})
	}
}
