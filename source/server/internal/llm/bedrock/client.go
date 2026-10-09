package bedrock

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/internal/usage"
)

// Config holds the Bedrock client configuration.
type Config struct {
	Region     string // required, e.g. "us-east-1"
	Model      string // Bedrock model id or inference-profile id
	AWSProfile string // optional ~/.aws named profile
	BaseURL    string // optional endpoint override (VPC/private)
}

// converseAPI is the subset of *bedrockruntime.Client the provider uses, so the
// non-streaming path is unit-testable with a fake.
type converseAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(ctx context.Context, in *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// Client implements inference.Provider over the Bedrock Converse API.
type Client struct {
	api   converseAPI
	model string
}

// NewClient builds a Client. Credentials resolve via the AWS default chain
// (env / ~/.aws / SSO / IAM). Region is required. Returns an error because the
// AWS config load can fail.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("bedrock: region is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.AWSProfile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.AWSProfile))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock: load AWS config: %w", err)
	}
	var policyClientErr error
	api := bedrockruntime.NewFromConfig(awsCfg, func(o *bedrockruntime.Options) {
		// Wrap only model-serving traffic, not the AWS credential-provider chain.
		o.HTTPClient, policyClientErr = policyHTTPClient(o.HTTPClient)
		if cfg.BaseURL != "" {
			o.BaseEndpoint = aws.String(cfg.BaseURL)
		}
	})
	if policyClientErr != nil {
		return nil, policyClientErr
	}
	return &Client{api: api, model: cfg.Model}, nil
}

// Preserve AWS's CA bundle, dialer, timeouts and standalone redirect behavior.
// This runs after the SDK resolves its HTTP defaults, rather than replacing them.
func policyHTTPClient(base bedrockruntime.HTTPClient) (*http.Client, error) {
	if buildable, ok := base.(*awshttp.BuildableClient); ok {
		base = buildable.Freeze()
	}
	client, ok := base.(*http.Client)
	if !ok {
		return nil, fmt.Errorf("bedrock: unsupported HTTP client for model authorization")
	}
	return modelpolicy.Client(client, "bedrock", "external", modelpolicy.Bedrock), nil
}

func (c *Client) Name() string { return "bedrock" }

func (c *Client) Capabilities() inference.Capabilities {
	return inference.Capabilities{
		SupportsTools:         true,
		SupportsParallelTools: true,
		SupportsCaching:       false,
		SupportsVision:        true,
	}
}

func modelOr(def, override string) string {
	if override != "" {
		return override
	}
	return def
}

// Chat sends a non-streaming Converse request and maps the output to blocks.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	msgs, err := messagesToConverse(ctx, req.Messages)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	m := &attemptMiddleware{model: modelOr(c.model, req.Model)}
	var opts []func(*bedrockruntime.Options)
	if usage.AttemptsEnabled(ctx) {
		opts = append(opts, m.option)
	}
	out, err := c.api.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId:         aws.String(modelOr(c.model, req.Model)),
		Messages:        msgs,
		System:          systemBlocks(req.System),
		ToolConfig:      toolsToConverse(req.Tools),
		InferenceConfig: inferenceConfig(req),
	}, opts...)
	if err != nil {
		return llm.ChatResponse{}, fmt.Errorf("bedrock: converse: %w", err)
	}
	resp := llm.ChatResponse{Model: m.model, Usage: normalizedUsage(out.Usage), StopReason: string(out.StopReason)}
	if m, ok := out.Output.(*types.ConverseOutputMemberMessage); ok {
		resp.Blocks = blocksFromConverse(m.Value)
	}
	if out.Usage != nil {
		resp.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
		resp.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
	}
	return resp, nil
}

// StreamChat opens a Converse stream and returns an llm.StreamReader.
func (c *Client) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	msgs, err := messagesToConverse(ctx, req.Messages)
	if err != nil {
		return nil, err
	}
	m := &attemptMiddleware{model: modelOr(c.model, req.Model)}
	var opts []func(*bedrockruntime.Options)
	if usage.AttemptsEnabled(ctx) {
		opts = append(opts, m.option)
	}
	out, err := c.api.ConverseStream(ctx, &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(modelOr(c.model, req.Model)),
		Messages:        msgs,
		System:          systemBlocks(req.System),
		ToolConfig:      toolsToConverse(req.Tools),
		InferenceConfig: inferenceConfig(req),
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock: converse stream: %w", err)
	}
	return m.streamAttempt.TrackStream(newStreamReader(out.GetStream())), nil
}
