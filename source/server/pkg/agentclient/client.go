// Package agentclient wraps the gRPC AgentClient with channel-based streaming
// suitable for a Bubble Tea program. Provides hybrid transport: connect to an
// existing agent, or auto-launch one on the same host.
package agentclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cercano/source/server/pkg/proto"
)

// cercanoBinaryName is the sibling server binary's filename: on Windows it
// carries the required .exe extension; elsewhere it's extension-less.
var cercanoBinaryName = func() string {
	if runtime.GOOS == "windows" {
		return "cercano.exe"
	}
	return "cercano"
}()

// grpcConnAlias is used by reconnect.go so its helpers can hold a
// package-scoped conn type without importing grpc directly. The value
// is always the same underlying *grpc.ClientConn we hold here.
type grpcConnAlias = grpc.ClientConn

// Client owns the gRPC connection and exposes a high-level streaming API.
//
// conn + agent are protected by connMu so the reconnect flow can swap
// them atomically when the underlying server dies and comes back. All
// SDK methods read c.conn / c.agent through readConn — never touch the
// fields directly outside that helper.
type Client struct {
	connMu       sync.Mutex
	conn         *grpc.ClientConn
	agent        proto.AgentClient
	AutoLaunched bool   // true if Dial spawned a new cercano process
	ServerLog    string // path to the auto-launched server's log file, if any

	// addr is the dial target — kept so reconnect() can redial after a
	// crash without threading it through every callsite.
	addr string

	// Connection-state observation (see reconnect.go).
	stateMu     sync.Mutex
	state       ConnState
	stateBroker *stateBroker
	stopWatch   chan struct{} // closed by Close() to stop watchConn
	// reconnectMu single-flights recovery: watchConn and the
	// SubscribeEvents drain loop can both detect a dead transport and
	// call reconnect() concurrently; without this they'd run competing
	// retry loops, each spawning replacement servers.
	reconnectMu sync.Mutex
}

// InlineImage is a user-attached image sent with a chat turn. Index matches the
// "[image <Index>]" marker in the input text.
type InlineImage struct {
	Index     int32
	Data      []byte
	MediaType string
}

// Dial connects to a cercano agent. If no listener exists at addr, auto-launches
// `cercano` (the agent binary) in the background and waits for it to come up.
// The spawned server outlives the CLI so VS Code / Zed / other clients can share it.
//
// After a successful dial, Dial starts a background goroutine that
// watches the underlying connection for TRANSIENT_FAILURE (server crash
// / network partition) and automatically reconnects — respawning the
// server if necessary. See reconnect.go for the recovery flow.
func Dial(ctx context.Context, addr string) (*Client, error) {
	// First try: short connect against an existing server.
	if c, err := connect(ctx, addr, 600*time.Millisecond); err == nil {
		c.addr = addr
		c.stopWatch = make(chan struct{})
		go c.watchConn(context.Background())
		return c, nil
	}

	logPath, err := autoLaunchServer(addr)
	if err != nil {
		return nil, fmt.Errorf("no agent at %s and could not auto-launch: %w", addr, err)
	}
	if err := waitForPort(addr, 8*time.Second); err != nil {
		return nil, fmt.Errorf("auto-launched cercano did not bind %s in time (log: %s): %w", addr, logPath, err)
	}
	c, err := connect(ctx, addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("auto-launched cercano bound %s but gRPC dial failed (log: %s): %w", addr, logPath, err)
	}
	c.AutoLaunched = true
	c.ServerLog = logPath
	c.addr = addr
	c.stopWatch = make(chan struct{})
	go c.watchConn(context.Background())
	return c, nil
}

func connect(ctx context.Context, addr string, timeout time.Duration) (*Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// 64 MiB matches the server recv limit; also future-proofs large responses.
	const maxGRPCMsgBytes = 64 << 20
	conn, err := grpc.DialContext(dialCtx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(maxGRPCMsgBytes),
			grpc.MaxCallRecvMsgSize(maxGRPCMsgBytes),
		),
	)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, agent: proto.NewAgentClient(conn)}, nil
}

// autoLaunchServer spawns `cercano` (the agent server binary) in the background.
// Looks for the binary next to the running cercano-cli executable first, then $PATH.
// Output is redirected to a log file under $TMPDIR; the process is detached via setsid
// so it survives a CLI crash and remains available to other clients.
func autoLaunchServer(addr string) (string, error) {
	bin, err := findCercanoBinary()
	if err != nil {
		return "", err
	}
	logPath := filepath.Join(os.TempDir(), "cercano-server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", fmt.Errorf("open log %s: %w", logPath, err)
	}
	cmd := exec.Command(bin, "agent")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachSysProcAttr() // detach from CLI's tty/process group
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return "", fmt.Errorf("start %s: %w", bin, err)
	}
	// Release; don't reap. Server lives independently.
	_ = cmd.Process.Release()
	return logPath, nil
}

func findCercanoBinary() (string, error) {
	// 1. Sibling to the running cercano-cli executable (dev build, brew install).
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), cercanoBinaryName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	// 2. $PATH.
	if p, err := exec.LookPath("cercano"); err == nil {
		return p, nil
	}
	return "", errors.New("`cercano` binary not found (looked next to cercano-cli and in $PATH)")
}

func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			// Brief sip to let the gRPC server finish registering services.
			time.Sleep(200 * time.Millisecond)
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("port did not become listenable within %s", timeout)
}

// Close releases the gRPC connection and stops the background
// connection-state watcher. Safe to call on a nil / never-Dialed client.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.stopWatch != nil {
		select {
		case <-c.stopWatch:
			// already closed
		default:
			close(c.stopWatch)
		}
	}
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// Config is the current runtime config reported by the agent.
type Config struct {
	OllamaURL             string
	OpenRuntime           string
	OpenModel             string
	EmbeddingModel        string
	CloudProvider         string
	CloudModel            string
	CloudBaseURL          string
	CloudAPIKeySet        bool
	CloudState            string // "ok" | "absent" | "error"
	Port                  string
	LocusMode             string
	WatchdogEnabled       bool
	WatchdogEcho          bool
	WatchdogMode          string
	WatchdogChecks        []string
	WatchdogEscalateAfter int
	// ElideToolResults: mechanical superseded-tool-result dedup on the
	// assembled history. Development Tools section in the settings UI.
	ElideToolResults bool
	// LossyToolElision: recency-window elision — stub older tool_results,
	// keep only the last N in full. Wider savings than ElideToolResults but
	// not byte-preserving.
	LossyToolElision bool
	// Retention horizons in days; KeepForever gates any aging.
	RawRetentionDays       int
	CompactedRetentionDays int
	KeepForever            bool
	// CompactionEnabled is the master switch for the summarization pass.
	CompactionEnabled bool
	// ToolElisionOnly makes compaction passes advance the elision floor
	// instead of calling the summarizer (LLM-free).
	ToolElisionOnly bool
	// ToolLoopMaxIterations caps LLM round-trips per turn; -1 means unlimited.
	ToolLoopMaxIterations int
	// ModelTiers is the taxonomy's non-empty slots keyed "<tier>.<provider>";
	// ModelsDefaultProvider is the preferred side ("cloud"|"open"|"").
	ModelTiers            map[string]string
	ModelsDefaultProvider string
	// mistral.rs runtime settings (Runtime tab).
	MistralRSISQ              string
	MistralRSPagedAttn        string
	MistralRSPAMemoryFraction string
}

// GetConfig fetches the agent's current runtime config.
func (c *Client) GetConfig(ctx context.Context) (*Config, error) {
	resp, err := c.agent.GetConfig(ctx, &proto.GetConfigRequest{})
	if err != nil {
		return nil, err
	}
	return &Config{
		OllamaURL:                 resp.GetOllamaUrl(),
		OpenRuntime:               resp.GetOpenRuntime(),
		OpenModel:                 resp.GetOpenModel(),
		EmbeddingModel:            resp.GetEmbeddingModel(),
		CloudProvider:             resp.GetCloudProvider(),
		CloudModel:                resp.GetCloudModel(),
		CloudBaseURL:              resp.GetCloudBaseUrl(),
		CloudAPIKeySet:            resp.GetCloudApiKeySet(),
		CloudState:                resp.GetCloudState(),
		Port:                      resp.GetPort(),
		LocusMode:                 resp.GetLocusMode(),
		WatchdogEnabled:           resp.GetWatchdogEnabled(),
		WatchdogEcho:              resp.GetWatchdogEcho(),
		WatchdogMode:              resp.GetWatchdogMode(),
		WatchdogChecks:            splitChecks(resp.GetWatchdogChecks()),
		WatchdogEscalateAfter:     atoiOr(resp.GetWatchdogEscalateAfter(), 0),
		ElideToolResults:          resp.GetElideToolResults(),
		LossyToolElision:          resp.GetLossyToolElision(),
		RawRetentionDays:          int(resp.GetRawRetentionDays()),
		CompactedRetentionDays:    int(resp.GetCompactedRetentionDays()),
		KeepForever:               resp.GetKeepForever(),
		CompactionEnabled:         resp.GetCompactionEnabled(),
		ToolElisionOnly:           resp.GetToolElisionOnly(),
		ToolLoopMaxIterations:     int(resp.GetToolLoopMaxIterations()),
		ModelTiers:                resp.GetModelTiers(),
		ModelsDefaultProvider:     resp.GetModelsDefaultProvider(),
		MistralRSISQ:              resp.GetMistralrsIsq(),
		MistralRSPagedAttn:        resp.GetMistralrsPagedAttn(),
		MistralRSPAMemoryFraction: resp.GetMistralrsPaMemoryFraction(),
	}, nil
}

// ConfigUpdate is a sparse patch sent to UpdateConfig — only non-empty fields
// are applied. Use SetCloudAPIKey to explicitly send an empty key when the
// proxy handles auth.
type ConfigUpdate struct {
	OllamaURL   string
	OpenRuntime string
	OpenModel   string
	// OpenDefaultModel sets llama_server.default_model — the GGUF the managed
	// runtime loads. Distinct from OpenModel so a GGUF pick never clobbers
	// the user's ollama tag.
	OpenDefaultModel      string
	CloudProvider         string
	CloudModel            string
	CloudAPIKey           string
	CloudBaseURL          string
	LocusMode             string
	WatchdogEnabled       string // "" = unchanged, "true"/"false"
	WatchdogEcho          string // "" = unchanged, "true"/"false"
	WatchdogMode          string // "" = unchanged, "challenge-and-justify"/"strict"
	WatchdogChecks        string // "" = unchanged, "-" = empty list, else comma-separated
	WatchdogEscalateAfter string // "" = unchanged, else integer >= 1
	// ElideToolResults is string-encoded to preserve the sparse-patch
	// convention: "" = leave unchanged, "true" / "false" = apply.
	ElideToolResults string
	// LossyToolElision — same encoding as ElideToolResults.
	LossyToolElision string
	// Retention — string-encoded to preserve sparse-patch semantics. Days
	// must parse as non-negative integers; KeepForever is "true" / "false".
	RawRetentionDays       string
	CompactedRetentionDays string
	KeepForever            string
	// CompactionEnabled — sparse-patch bool. "" | "true" | "false".
	CompactionEnabled string
	// ToolElisionOnly — sparse-patch bool. "" | "true" | "false".
	ToolElisionOnly string
	// ToolLoopMaxIterations — sparse-patch int. "" = unchanged, -1 = unlimited.
	ToolLoopMaxIterations string
	// Model taxonomy sparse-patch: ModelTierKey is "default_provider" or
	// "<tier>.<provider>"; ModelTierValue is the model id ("-" clears).
	// Empty key = unchanged.
	ModelTierKey   string
	ModelTierValue string
	// mistral.rs runtime settings (Runtime tab). Sparse-patch: "" = unchanged,
	// "-" clears.
	MistralRSISQ              string
	MistralRSPagedAttn        string
	MistralRSPAMemoryFraction string
}

// RuntimeStatus is the provider-neutral model/runtime dashboard snapshot.
type RuntimeStatus struct {
	Models    []RuntimeModel
	Instances []RuntimeInstance
	Endpoints []RuntimeEndpoint
	Logs      []RuntimeLogEntry
}

type RuntimeModel struct {
	ID                 string
	DisplayName        string
	Runtime            string
	Source             string
	Path               string
	Format             string
	Family             string
	Quantization       string
	SizeBytes          int64
	ModifiedAt         time.Time
	DownloadState      string
	DownloadURL        string
	DownloadedBytes    int64
	DownloadTotalBytes int64
	DownloadError      string
	RuntimeState       string
	SupportsChat       bool
	SupportsEmbed      bool
	SupportsTools      bool
	CatalogID          string
	Active             bool
	// KVBytesPerToken/MaxContextTokens are pre-warmed RAM-estimation
	// numbers embedded by the server (0 = not warmed yet; callers fall
	// back to GetModelRAMEstimate).
	KVBytesPerToken  int64
	MaxContextTokens int64
}

type RuntimeInstance struct {
	ID           string
	Runtime      string
	ModelID      string
	State        string
	PID          int
	Address      string
	Port         int
	Endpoint     string
	StartedAt    time.Time
	ReadyAt      time.Time
	RestartCount int
	LastExitCode int
	LastError    string
	LogPath      string
}

type RuntimeEndpoint struct {
	ID            string
	Kind          string
	DisplayName   string
	BaseURL       string
	Scope         string
	State         string
	ActiveRoles   []string
	Models        []string
	LastCheckedAt time.Time
	LatencyMS     int64
	LastError     string
	AuthState     string
}

type RuntimeLogEntry struct {
	Timestamp time.Time
	Source    string
	Level     string
	RuntimeID string
	ModelID   string
	Message   string
}

type RuntimeLogMsg struct {
	Entry RuntimeLogEntry
	Err   error
}

// ConversationInfo is a persisted conversation summary returned by ListConversations.
type ConversationInfo struct {
	ID             string
	Title          string
	ProjectDir     string
	Model          string
	StartedAt      time.Time
	LastTurnAt     time.Time
	TurnCount      int
	Recap          string
	RecapUpdatedAt time.Time
}

// PersistedTurn is one stored role-emission returned by ResumeConversation.
type PersistedTurn struct {
	ID             string
	ConversationID string
	Role           string
	Content        string
	TokensIn       int
	TokensOut      int
	LatencyMs      int
	CreatedAt      time.Time
}

// ListConversations returns the persisted conversation history.
func (c *Client) ListConversations(ctx context.Context, projectDir string, limit int) ([]ConversationInfo, error) {
	resp, err := c.agent.ListConversations(ctx, &proto.ListConversationsRequest{
		ProjectDir: projectDir,
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ConversationInfo, 0, len(resp.GetConversations()))
	for _, c := range resp.GetConversations() {
		out = append(out, ConversationInfo{
			ID:             c.GetId(),
			Title:          c.GetTitle(),
			ProjectDir:     c.GetProjectDir(),
			Model:          c.GetModel(),
			StartedAt:      time.Unix(c.GetStartedAt(), 0),
			LastTurnAt:     time.Unix(c.GetLastTurnAt(), 0),
			TurnCount:      int(c.GetTurnCount()),
			Recap:          c.GetRecap(),
			RecapUpdatedAt: time.Unix(c.GetRecapUpdatedAt(), 0),
		})
	}
	return out, nil
}

// ResumeConversation loads the turns of a persisted conversation and
// rehydrates the in-memory session store on the agent.
func (c *Client) ResumeConversation(ctx context.Context, conversationID string) ([]PersistedTurn, error) {
	resp, err := c.agent.ResumeConversation(ctx, &proto.ResumeConversationRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}
	out := make([]PersistedTurn, 0, len(resp.GetTurns()))
	for _, t := range resp.GetTurns() {
		out = append(out, PersistedTurn{
			ID:             t.GetId(),
			ConversationID: t.GetConversationId(),
			Role:           t.GetRole(),
			Content:        t.GetContent(),
			TokensIn:       int(t.GetTokensIn()),
			TokensOut:      int(t.GetTokensOut()),
			LatencyMs:      int(t.GetLatencyMs()),
			CreatedAt:      time.Unix(t.GetCreatedAt(), 0),
		})
	}
	return out, nil
}

// SubAgentInfo is one persisted sub-agent (dispatch) conversation spawned
// under a parent conversation. Enough for the CLI to recreate its chat tab
// (title + granted tools); the transcript is fetched separately by id via
// ResumeConversation.
type SubAgentInfo struct {
	ID           string
	ParentID     string
	Title        string
	GrantedTools []string
}

// ListSubAgents returns the persisted sub-agent conversations spawned under a
// parent conversation, in spawn order. The CLI calls this on resume to reopen
// each sub-agent chat tab, then fetches each transcript via ResumeConversation.
func (c *Client) ListSubAgents(ctx context.Context, parentID string) ([]SubAgentInfo, error) {
	resp, err := c.agent.ListSubAgents(ctx, &proto.ListSubAgentsRequest{ParentId: parentID})
	if err != nil {
		return nil, err
	}
	out := make([]SubAgentInfo, 0, len(resp.GetSubagents()))
	for _, sa := range resp.GetSubagents() {
		out = append(out, SubAgentInfo{
			ID:           sa.GetId(),
			ParentID:     sa.GetParentId(),
			Title:        sa.GetTitle(),
			GrantedTools: sa.GetGrantedTools(),
		})
	}
	return out, nil
}

// DismissSubAgent marks a sub-agent conversation dismissed so a resumed CLI
// does not reopen its tab. Best-effort from the caller's side.
func (c *Client) DismissSubAgent(ctx context.Context, conversationID string) error {
	_, err := c.agent.DismissSubAgent(ctx, &proto.DismissSubAgentRequest{ConversationId: conversationID})
	return err
}

// DeleteConversation removes a persisted conversation.
func (c *Client) DeleteConversation(ctx context.Context, conversationID string) error {
	_, err := c.agent.DeleteConversation(ctx, &proto.DeleteConversationRequest{ConversationId: conversationID})
	return err
}

// RenameConversation sets a custom title on a persisted conversation.
func (c *Client) RenameConversation(ctx context.Context, conversationID, title string) error {
	_, err := c.agent.RenameConversation(ctx, &proto.RenameConversationRequest{
		ConversationId: conversationID,
		Title:          title,
	})
	return err
}

// GetConversation fetches a single conversation's metadata including its recap.
func (c *Client) GetConversation(ctx context.Context, conversationID string) (ConversationInfo, error) {
	resp, err := c.agent.GetConversation(ctx, &proto.GetConversationRequest{ConversationId: conversationID})
	if err != nil {
		return ConversationInfo{}, err
	}
	return ConversationInfo{
		ID:             resp.GetId(),
		Title:          resp.GetTitle(),
		ProjectDir:     resp.GetProjectDir(),
		Model:          resp.GetModel(),
		StartedAt:      time.Unix(resp.GetStartedAt(), 0),
		LastTurnAt:     time.Unix(resp.GetLastTurnAt(), 0),
		TurnCount:      int(resp.GetTurnCount()),
		Recap:          resp.GetRecap(),
		RecapUpdatedAt: time.Unix(resp.GetRecapUpdatedAt(), 0),
	}, nil
}

// ToolInfo is the registry summary returned by ListTools.
type ToolInfo struct {
	Name        string
	Description string
	Permission  string // "R" | "W" | "X"
	Schema      string // JSON Schema
	Destructive bool   // display-only ⚠ hint (MCP destructiveHint)
}

// ListTools enumerates the agent's registered tools.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	resp, err := c.agent.ListTools(ctx, &proto.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]ToolInfo, 0, len(resp.GetTools()))
	for _, t := range resp.GetTools() {
		out = append(out, ToolInfo{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			Permission:  t.GetPermission(),
			Schema:      t.GetSchema(),
			Destructive: t.GetDestructive(),
		})
	}
	return out, nil
}

// ToolResult mirrors the agent-side Result, shaped for the CLI's renderer.
type ToolResult struct {
	Type      string // "rows" | "text" | "json"
	Text      string
	RowsJSON  string // marshalled []map[string]any
	JSON      string
	Truncated bool
	Note      string
	Error     string
}

// InvokeTool runs the named tool with JSON args.
func (c *Client) InvokeTool(ctx context.Context, name, argsJSON string) (*ToolResult, error) {
	resp, err := c.agent.InvokeTool(ctx, &proto.InvokeToolRequest{Name: name, ArgsJson: argsJSON})
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Type:      resp.GetResultType(),
		Text:      resp.GetText(),
		RowsJSON:  resp.GetRowsJson(),
		JSON:      resp.GetJson(),
		Truncated: resp.GetTruncated(),
		Note:      resp.GetNote(),
		Error:     resp.GetError(),
	}, nil
}

// ContextUsage is the cumulative token usage for a conversation against the
// active model's context-window size.
type ContextUsage struct {
	TokensUsed int
	ModelMax   int
	Percent    float64
	RawTokens  int
	Compacting bool
}

// GetContextUsage fetches the live context-window meter for a conversation.
func (c *Client) GetContextUsage(ctx context.Context, conversationID string) (*ContextUsage, error) {
	resp, err := c.agent.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}
	return &ContextUsage{
		TokensUsed: int(resp.GetTokensUsed()),
		ModelMax:   int(resp.GetModelMax()),
		Percent:    resp.GetPercent(),
		RawTokens:  int(resp.GetRawTokens()),
		Compacting: resp.GetCompacting(),
	}, nil
}

// ToolCallDetail is the full args + result body for one tool call, fetched
// lazily when the CLI expands a scrollback tool entry. Found reports whether
// the tool_use block was located; Result may be empty when the call is still
// in flight.
type ToolCallDetail struct {
	Found     bool
	ToolName  string
	ArgsJSON  string
	Result    string
	IsError   bool
	StartLine int // 1-based first line of an edit/write (0 = n/a)
}

// GetToolCall fetches the full args and result body for a single tool call in
// a conversation, by tool_use_id. Backs the CLI's expand-on-click of a folded
// scrollback tool entry.
func (c *Client) GetToolCall(ctx context.Context, conversationID, toolUseID string) (*ToolCallDetail, error) {
	resp, err := c.agent.GetToolCall(ctx, &proto.GetToolCallRequest{ConversationId: conversationID, ToolUseId: toolUseID})
	if err != nil {
		return nil, err
	}
	return &ToolCallDetail{
		Found:     resp.GetFound(),
		ToolName:  resp.GetToolName(),
		ArgsJSON:  resp.GetArgsJson(),
		Result:    resp.GetResult(),
		IsError:   resp.GetIsError(),
		StartLine: int(resp.GetStartLine()),
	}, nil
}

// CompactionState mirrors GetCompactionStateResponse for the /c viewer.
type CompactionState struct {
	FrozenThrough       int64
	FrozenTurns         int
	LiveTurns           int
	CompactedSegments   int
	RawTokens           int
	SentTokens          int
	ConsolidatedSummary string
	Compacting          bool
}

// ElideContext stubs every tool-result body in the conversation's context up
// to now (the /elide-context command). In-memory and send-view only: stored
// raw turns are untouched and the effect resets on agent restart. Returns
// sent-view token counts before/after and how many results were stubbed.
func (c *Client) ElideContext(ctx context.Context, conversationID string) (pre, post, stubbed int, err error) {
	resp, err := c.agent.ElideContext(ctx, &proto.ElideContextRequest{ConversationId: conversationID})
	if err != nil {
		return 0, 0, 0, err
	}
	return int(resp.GetPreTokens()), int(resp.GetPostTokens()), int(resp.GetStubbed()), nil
}

func (c *Client) GetCompactionState(ctx context.Context, conversationID string) (*CompactionState, error) {
	resp, err := c.agent.GetCompactionState(ctx, &proto.GetCompactionStateRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}
	return &CompactionState{
		FrozenThrough:       resp.GetFrozenThrough(),
		FrozenTurns:         int(resp.GetFrozenTurns()),
		LiveTurns:           int(resp.GetLiveTurns()),
		CompactedSegments:   int(resp.GetCompactedSegments()),
		RawTokens:           int(resp.GetRawTokens()),
		SentTokens:          int(resp.GetSentTokens()),
		ConsolidatedSummary: resp.GetConsolidatedSummary(),
		Compacting:          resp.GetCompacting(),
	}, nil
}

// SuggestNextPrompt asks the local co-processor for a one-line follow-up the
// user might send next, given the conversation so far. Returns "" on any
// failure — the CLI treats empty as "no suggestion" and hides the ghost text.
func (c *Client) SuggestNextPrompt(ctx context.Context, conversationID string) (string, error) {
	resp, err := c.agent.SuggestNextPrompt(ctx, &proto.SuggestNextPromptRequest{ConversationId: conversationID})
	if err != nil {
		return "", err
	}
	return resp.GetSuggestion(), nil
}

// ExportContext returns the full uncapped raw history as a JSON []llm.Message.
func (c *Client) ExportContext(ctx context.Context, conversationID string) (string, error) {
	resp, err := c.agent.ExportContext(ctx, &proto.ExportContextRequest{ConversationId: conversationID})
	if err != nil {
		return "", err
	}
	return resp.GetJson(), nil
}

// ExportTrajectoryOptions controls the agent-owned ATIF trajectory bundle export.
type ExportTrajectoryOptions struct {
	ConversationID string
	OutPath        string
	Format         string // infer|directory|zip
	RedactionMode  string // default|none
	IncludeLogs    bool
	Overwrite      bool
}

// ExportTrajectoryEvent is a simplified progress/completion event from the
// streaming ExportTrajectory RPC.
type ExportTrajectoryEvent struct {
	Kind          string // progress|warning|completed|failed
	Phase         string
	Message       string
	Code          string
	OutputPath    string
	ManifestPath  string
	ArtifactCount int
	SubagentCount int
}

// ExportTrajectory streams an agent-owned trajectory bundle export. The CLI
// should use this rather than reading conversations.db or writing bundles.
func (c *Client) ExportTrajectory(ctx context.Context, opts ExportTrajectoryOptions) (<-chan ExportTrajectoryEvent, <-chan error) {
	events := make(chan ExportTrajectoryEvent, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		stream, err := c.agent.ExportTrajectory(ctx, &proto.ExportTrajectoryRequest{
			ConversationId: opts.ConversationID,
			OutPath:        opts.OutPath,
			Format:         opts.Format,
			RedactionMode:  opts.RedactionMode,
			IncludeLogs:    opts.IncludeLogs,
			Overwrite:      opts.Overwrite,
		})
		if err != nil {
			errs <- err
			return
		}
		for {
			ev, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				errs <- err
				return
			}
			switch p := ev.GetPayload().(type) {
			case *proto.ExportTrajectoryEvent_Progress:
				events <- ExportTrajectoryEvent{Kind: "progress", Phase: p.Progress.GetPhase(), Message: p.Progress.GetMessage()}
			case *proto.ExportTrajectoryEvent_Warning:
				events <- ExportTrajectoryEvent{Kind: "warning", Code: p.Warning.GetCode(), Message: p.Warning.GetMessage()}
			case *proto.ExportTrajectoryEvent_Completed:
				events <- ExportTrajectoryEvent{Kind: "completed", OutputPath: p.Completed.GetOutputPath(), ManifestPath: p.Completed.GetManifestPath(), ArtifactCount: int(p.Completed.GetArtifactCount()), SubagentCount: int(p.Completed.GetSubagentCount())}
			case *proto.ExportTrajectoryEvent_Failed:
				events <- ExportTrajectoryEvent{Kind: "failed", Code: p.Failed.GetCode(), Message: p.Failed.GetMessage()}
			}
		}
	}()
	return events, errs
}

// ContextTurn is one display-ready turn summary from GetConversationTurns.
type ContextTurn struct {
	ID        string
	Role      string
	Kind      string
	Preview   string
	Body      string
	Truncated bool
	EstTokens int
	// Tool metadata from the turn's first tool block (see ContextTurn proto):
	// lets the /c viewer reuse the main chat's rich tool renderers.
	ToolName   string // tool_use turns: the tool's name
	ToolUseID  string // tool_use turns: the call's correlation id
	ToolArgs   string // tool_use turns: input JSON, capped at 4 KB
	ToolUseRef string // tool_result turns: originating call's id
	// ToolStartLine (tool_result turns): 1-based first line of an edit/write
	// in the target file, recorded at execute time. 0 = not applicable.
	ToolStartLine int
}

// GetConversationTurns returns side-effect-free turn summaries for the /c viewer.
func (c *Client) GetConversationTurns(ctx context.Context, conversationID string) ([]ContextTurn, error) {
	resp, err := c.agent.GetConversationTurns(ctx, &proto.GetConversationTurnsRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}
	out := make([]ContextTurn, 0, len(resp.GetTurns()))
	for _, t := range resp.GetTurns() {
		out = append(out, ContextTurn{
			ID:            t.GetId(),
			Role:          t.GetRole(),
			Kind:          t.GetKind(),
			Preview:       t.GetPreview(),
			Body:          t.GetBody(),
			Truncated:     t.GetTruncated(),
			EstTokens:     int(t.GetEstTokens()),
			ToolName:      t.GetToolName(),
			ToolUseID:     t.GetToolUseId(),
			ToolArgs:      t.GetToolArgs(),
			ToolUseRef:    t.GetToolUseRef(),
			ToolStartLine: int(t.GetToolStartLine()),
		})
	}
	return out, nil
}

// Proposal is the set of turn IDs proposed for deletion and the model's rationale.
type Proposal struct {
	DeleteIDs []string
	Rationale string
}

// ProposeContextEdit asks the agent to analyse a conversation and propose turns
// to delete based on the given instruction. Read-only — no turns are deleted.
func (c *Client) ProposeContextEdit(ctx context.Context, conversationID, instruction string) (Proposal, error) {
	resp, err := c.agent.ProposeContextEdit(ctx, &proto.ProposeContextEditRequest{
		ConversationId: conversationID, Instruction: instruction,
	})
	if err != nil {
		return Proposal{}, err
	}
	return Proposal{DeleteIDs: resp.GetDeleteIds(), Rationale: resp.GetRationale()}, nil
}

// DeleteConversationTurns hard-deletes the named turns from a conversation and
// returns the number of turns deleted.
func (c *Client) DeleteConversationTurns(ctx context.Context, conversationID string, ids []string) (int, error) {
	resp, err := c.agent.DeleteConversationTurns(ctx, &proto.DeleteConversationTurnsRequest{
		ConversationId: conversationID, TurnId: ids,
	})
	if err != nil {
		return 0, err
	}
	return int(resp.GetDeleted()), nil
}

func (c *Client) GetRuntimeStatus(ctx context.Context) (*RuntimeStatus, error) {
	resp, err := c.agent.GetRuntimeStatus(ctx, &proto.GetRuntimeStatusRequest{})
	if err != nil {
		return nil, err
	}
	return &RuntimeStatus{
		Models:    mapRuntimeModels(resp.GetModels()),
		Instances: mapRuntimeInstances(resp.GetInstances()),
		Endpoints: mapRuntimeEndpoints(resp.GetEndpoints()),
		Logs:      mapRuntimeLogs(resp.GetLogs()),
	}, nil
}

// RuntimeModelCatalog is the full downloadable-model catalog: locally
// enrolled models, the hardcoded llama-server catalog, and (when the
// online catalog manager is attached server-side) Ollama's public
// library. CatalogUpdatedAt is zero when no online fetch has ever
// succeeded.
type RuntimeModelCatalog struct {
	Models           []RuntimeModel
	CatalogUpdatedAt time.Time
	// SystemRAMBytes is the server machine's total physical memory,
	// for rendering fit verdicts against embedded estimates. 0 when
	// the platform probe failed.
	SystemRAMBytes int64
	// RecommendedOpenModels maps each capability tier to the curated
	// open model the server recommends for this machine's RAM, keyed by
	// the stable inventory id "llama_server:catalog:<bareID>". Empty when
	// the curated catalog failed to load. The setup wizard autofills its
	// open tier picks from this so every pick is gate-compatible.
	RecommendedOpenModels map[string]string
}

func (c *Client) ListRuntimeModels(ctx context.Context) (RuntimeModelCatalog, error) {
	resp, err := c.agent.ListRuntimeModels(ctx, &proto.ListRuntimeModelsRequest{})
	if err != nil {
		return RuntimeModelCatalog{}, err
	}
	out := RuntimeModelCatalog{
		Models:                mapRuntimeModels(resp.GetModels()),
		SystemRAMBytes:        resp.GetSystemRamBytes(),
		RecommendedOpenModels: resp.GetRecommendedOpenModels(),
	}
	if s := resp.GetCatalogUpdatedAt(); s != "" {
		if t, terr := time.Parse(time.RFC3339, s); terr == nil {
			out.CatalogUpdatedAt = t
		}
	}
	return out, nil
}

func (c *Client) ListRuntimeEndpoints(ctx context.Context) ([]RuntimeEndpoint, error) {
	resp, err := c.agent.ListRuntimeEndpoints(ctx, &proto.ListRuntimeEndpointsRequest{})
	if err != nil {
		return nil, err
	}
	return mapRuntimeEndpoints(resp.GetEndpoints()), nil
}

func (c *Client) StartRuntimeModel(ctx context.Context, runtimeName, modelID string) (*RuntimeInstance, error) {
	resp, err := c.agent.StartRuntimeModel(ctx, &proto.StartRuntimeModelRequest{
		Runtime: runtimeName,
		ModelId: modelID,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf("%s", resp.GetError())
	}
	instance := mapRuntimeInstance(resp.GetInstance())
	return &instance, nil
}

func (c *Client) StopRuntimeModel(ctx context.Context, instanceID string) error {
	resp, err := c.agent.StopRuntimeModel(ctx, &proto.StopRuntimeModelRequest{InstanceId: instanceID})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

func (c *Client) RestartRuntime(ctx context.Context, instanceID, runtimeName, modelID string) (*RuntimeInstance, error) {
	resp, err := c.agent.RestartRuntime(ctx, &proto.RestartRuntimeRequest{
		InstanceId: instanceID,
		Runtime:    runtimeName,
		ModelId:    modelID,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf("%s", resp.GetError())
	}
	instance := mapRuntimeInstance(resp.GetInstance())
	return &instance, nil
}

// DownloadRuntimeModel starts (or resumes) a model download. catalogID
// is only needed for online-catalog entries that aren't enrolled with
// the runtime manager yet (e.g. "qwen2.5-coder:7b" or a bare family
// name, which the server defaults to the :latest tag); pass "" for
// already-enrolled models.
func (c *Client) DownloadRuntimeModel(ctx context.Context, runtimeName, modelID, catalogID string) (*RuntimeModel, error) {
	resp, err := c.agent.DownloadRuntimeModel(ctx, &proto.DownloadRuntimeModelRequest{
		Runtime:   runtimeName,
		ModelId:   modelID,
		CatalogId: catalogID,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf("%s", resp.GetError())
	}
	model := mapRuntimeModel(resp.GetModel())
	return &model, nil
}

func (c *Client) CancelRuntimeModelDownload(ctx context.Context, runtimeName, modelID string) (*RuntimeModel, error) {
	resp, err := c.agent.CancelRuntimeModelDownload(ctx, &proto.CancelRuntimeModelDownloadRequest{
		Runtime: runtimeName,
		ModelId: modelID,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf("%s", resp.GetError())
	}
	model := mapRuntimeModel(resp.GetModel())
	return &model, nil
}

func (c *Client) DeleteRuntimeModel(ctx context.Context, runtimeName, modelID string) error {
	resp, err := c.agent.DeleteRuntimeModel(ctx, &proto.DeleteRuntimeModelRequest{
		Runtime: runtimeName,
		ModelId: modelID,
	})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

func (c *Client) StreamRuntimeLogs(ctx context.Context, tail int, source string) (<-chan RuntimeLogMsg, error) {
	stream, err := c.agent.StreamRuntimeLogs(ctx, &proto.StreamRuntimeLogsRequest{
		Tail:   int32(tail),
		Source: source,
	})
	if err != nil {
		return nil, err
	}
	out := make(chan RuntimeLogMsg, 16)
	go func() {
		defer close(out)
		for {
			entry, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				out <- RuntimeLogMsg{Err: err}
				return
			}
			out <- RuntimeLogMsg{Entry: mapRuntimeLog(entry)}
		}
	}()
	return out, nil
}

// AgentEvent is one item from the standing server->client SubscribeEvents
// stream. Exactly one of the typed fields is populated per event (the rest are
// zero / nil). Err is set if the stream itself failed and is the terminal
// event on this channel.
type AgentEvent struct {
	Mode              string             // populated by PermissionModeChanged events
	OpenRuntimeStatus *OpenRuntimeStatus // populated by OpenRuntimeStatusChanged events
	ConfigChanged     *ConfigChanged     // populated by ConfigChanged events
	Err               error
}

// ConfigChanged mirrors proto.ConfigChanged in the client SDK.
type ConfigChanged struct {
	Field string
	Value string
}

// OpenRuntimeStatus mirrors proto.OpenRuntimeStatus in the client SDK.
// Ok=true means the runtime is ready; ok=false + Missing tells the CLI what
// recovery flow to offer (install, model picker). SuggestedCommand is the
// shell command that would resolve Missing (e.g. "brew install llama.cpp").
type OpenRuntimeStatus struct {
	Ok               bool
	Runtime          string
	Missing          string
	Message          string
	SuggestedCommand string
	BinaryPath       string
	DefaultModel     string
	// Downloading is true when the runtime is not ready ONLY because its
	// default model is actively downloading. Distinct chip state from Missing:
	// the CLI renders "o: downloading" with no F1 prompt.
	Downloading bool
}

// SubscribeEvents opens the standing server->client event stream and returns a
// channel of agent events. The client holds this open for its whole session
// so the agent can push state changes instead of the client polling. The
// channel closes when the stream ends (server shutdown / disconnect).
func (c *Client) SubscribeEvents(ctx context.Context) (<-chan AgentEvent, error) {
	// Open the initial stream synchronously so callers get an error at
	// setup time if the very first attempt fails. Subsequent stream
	// errors during the drain loop route through the reconnect path.
	c.connMu.Lock()
	agent := c.agent
	c.connMu.Unlock()
	stream, err := agent.SubscribeEvents(ctx, &proto.SubscribeEventsRequest{})
	if err != nil {
		return nil, err
	}
	out := make(chan AgentEvent, 8)
	go c.drainSubscribeEvents(ctx, stream, out)
	return out, nil
}

// drainSubscribeEvents runs the event-drain loop for the whole client
// lifetime. On a transport error (server crash mid-stream) it waits for
// the background reconnect goroutine to restore the connection and then
// re-opens the SubscribeEvents stream. Only permanent failures (context
// cancelled, terminal ConnStateFailed, or a non-Unavailable RPC error)
// close the channel.
func (c *Client) drainSubscribeEvents(ctx context.Context, stream proto.Agent_SubscribeEventsClient, out chan<- AgentEvent) {
	defer close(out)
	for {
		if stream == nil {
			// Re-open after a reconnect. If reconnect ultimately
			// failed, currentState() will be Failed and we bail here.
			if c.currentState() == ConnStateFailed {
				out <- AgentEvent{Err: fmt.Errorf("agentclient: reconnect failed; event stream terminated")}
				return
			}
			c.connMu.Lock()
			agent := c.agent
			c.connMu.Unlock()
			s, err := agent.SubscribeEvents(ctx, &proto.SubscribeEventsRequest{})
			if err != nil {
				if isUnavailable(err) {
					// Still bad — trigger a reconnect and try again.
					if rErr := c.reconnect(ctx); rErr != nil {
						out <- AgentEvent{Err: rErr}
						return
					}
					continue
				}
				out <- AgentEvent{Err: err}
				return
			}
			stream = s
		}
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isUnavailable(err) {
				// Server death mid-stream. Ask the reconnector to
				// bring us back and loop to re-open the stream.
				stream = nil
				if rErr := c.reconnect(ctx); rErr != nil {
					out <- AgentEvent{Err: rErr}
					return
				}
				continue
			}
			out <- AgentEvent{Err: err}
			return
		}
		if pm := ev.GetPermissionModeChanged(); pm != nil {
			out <- AgentEvent{Mode: pm.GetMode()}
			continue
		}
		if lr := ev.GetOpenRuntimeStatusChanged(); lr != nil {
			out <- AgentEvent{OpenRuntimeStatus: openRuntimeStatusFromProto(lr.GetStatus())}
			continue
		}
		if cc := ev.GetConfigChanged(); cc != nil {
			out <- AgentEvent{ConfigChanged: &ConfigChanged{Field: cc.GetField(), Value: cc.GetValue()}}
			continue
		}
		// Unknown event types are silently dropped — clients that don't
		// recognise them should not error on forward-compat additions.
	}
}

// openRuntimeStatusFromProto converts the wire proto into the client-side
// struct. Nil-safe so callers can invoke unconditionally.
func openRuntimeStatusFromProto(p *proto.OpenRuntimeStatus) *OpenRuntimeStatus {
	if p == nil {
		return nil
	}
	return &OpenRuntimeStatus{
		Ok:               p.GetOk(),
		Runtime:          p.GetRuntime(),
		Missing:          p.GetMissing(),
		Message:          p.GetMessage(),
		SuggestedCommand: p.GetSuggestedCommand(),
		BinaryPath:       p.GetBinaryPath(),
		DefaultModel:     p.GetDefaultModel(),
		Downloading:      p.GetDownloading(),
	}
}

// InstallProgress is one frame from an InstallOpenRuntime stream. Frames
// with Done=false carry a Line of subprocess output. The stream terminates
// with a single Done=true frame carrying Ok+Error (Err populated only when
// the stream itself failed, distinct from a subprocess non-zero exit).
type InstallProgress struct {
	Line  string
	Done  bool
	Ok    bool
	Error string
	Err   error
}

// RegenProgress is one frame of a RegenerateContext stream. Err carries a
// transport-level failure; Error carries a server-reported one.
type RegenProgress struct {
	Line       string
	Done       bool
	Ok         bool
	Error      string
	PreTokens  int
	PostTokens int
	Err        error
}

// RegenerateContext runs compaction to completion on the server, streaming
// progress. incremental=false clears the derived state first (full rebuild
// from raw turns); incremental=true digests only the current backlog and
// keeps existing summaries (/compact). The returned channel closes after the
// terminal (Done) frame.
func (c *Client) RegenerateContext(ctx context.Context, conversationID string, incremental bool) (<-chan RegenProgress, error) {
	return c.regenContextStream(ctx, &proto.RegenerateContextRequest{ConversationId: conversationID, Incremental: incremental})
}

// ClearCompactedContext drops the conversation's derived compaction state
// without re-summarizing, so the next send-view is the full raw turn history
// (/clear-compacted-context — recovery when the compacted layer is bad). Same
// streaming contract as RegenerateContext.
func (c *Client) ClearCompactedContext(ctx context.Context, conversationID string) (<-chan RegenProgress, error) {
	return c.regenContextStream(ctx, &proto.RegenerateContextRequest{ConversationId: conversationID, ClearOnly: true})
}

func (c *Client) regenContextStream(ctx context.Context, req *proto.RegenerateContextRequest) (<-chan RegenProgress, error) {
	stream, err := c.agent.RegenerateContext(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan RegenProgress, 8)
	go func() {
		defer close(out)
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				out <- RegenProgress{Err: err}
				return
			}
			out <- RegenProgress{
				Line:       frame.GetLine(),
				Done:       frame.GetDone(),
				Ok:         frame.GetOk(),
				Error:      frame.GetError(),
				PreTokens:  int(frame.GetPreTokens()),
				PostTokens: int(frame.GetPostTokens()),
			}
			if frame.GetDone() {
				return
			}
		}
	}()
	return out, nil
}

// GetOpenRuntimeStatus fetches the local-runtime detection snapshot. When
// runtime is empty, the server reports against its currently-selected
// runtime (used by the CLI startup fetch that renders the chip). When
// runtime is set explicitly ("ollama" | "llama_server"), the server probes
// THAT runtime — used by the settings-page gate to check whether a switch
// is safe before dispatching UpdateConfig.
func (c *Client) GetOpenRuntimeStatus(ctx context.Context, runtime string) (*OpenRuntimeStatus, error) {
	resp, err := c.agent.GetOpenRuntimeStatus(ctx, &proto.GetOpenRuntimeStatusRequest{Runtime: runtime})
	if err != nil {
		return nil, err
	}
	return openRuntimeStatusFromProto(resp.GetStatus()), nil
}

// CatalogRefreshResult carries the outcome of a RefreshOnlineCatalog
// call. On success, Err is nil and UpdatedAt names the fresh fetch
// time. On failure, Err is set and UpdatedAt reflects the last
// SUCCESSFUL fetch so the CLI can still render staleness.
type CatalogRefreshResult struct {
	UpdatedAt  time.Time
	ModelCount int
	Err        error
}

// RefreshOnlineCatalog forces a fresh fetch of the model catalog from
// Ollama's public library. Blocks until the fetch completes so callers
// (typically the CLI dashboard's "R" refresh key) can update their
// timestamp display immediately on return.
func (c *Client) RefreshOnlineCatalog(ctx context.Context) CatalogRefreshResult {
	resp, err := c.agent.RefreshOnlineCatalog(ctx, &proto.RefreshOnlineCatalogRequest{})
	if err != nil {
		return CatalogRefreshResult{Err: err}
	}
	out := CatalogRefreshResult{ModelCount: int(resp.GetModelCount())}
	if s := resp.GetCatalogUpdatedAt(); s != "" {
		if t, terr := time.Parse(time.RFC3339, s); terr == nil {
			out.UpdatedAt = t
		}
	}
	if s := resp.GetError(); s != "" {
		out.Err = fmt.Errorf("%s", s)
	}
	return out
}

// InstalledOpenModel is one model reported by the active open-runtime
// engine's own listing (e.g. ollama's /api/tags) — models the engine
// can serve directly, as opposed to the runtime manager's GGUF
// inventory.
type InstalledOpenModel struct {
	Name       string
	SizeBytes  int64
	ModifiedAt string
}

// ListModels returns the active open runtime engine's installed
// models. For the ollama runtime this is the daemon's /api/tags list.
func (c *Client) ListModels(ctx context.Context) ([]InstalledOpenModel, error) {
	resp, err := c.agent.ListModels(ctx, &proto.ListModelsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]InstalledOpenModel, 0, len(resp.GetModels()))
	for _, m := range resp.GetModels() {
		out = append(out, InstalledOpenModel{
			Name:       m.GetName(),
			SizeBytes:  m.GetSize(),
			ModifiedAt: m.GetModifiedAt(),
		})
	}
	return out, nil
}

// ModelRAMEstimate carries the numbers needed to predict a model's
// runtime memory: total(ctx) ~= WeightsBytes + ctx*KVBytesPerToken +
// overhead. SystemRAMBytes lets the caller render a fit verdict
// without a second RPC; 0 means the platform probe failed.
type ModelRAMEstimate struct {
	WeightsBytes     int64
	KVBytesPerToken  int64
	MaxContextTokens int64
	Architecture     string
	SystemRAMBytes   int64
	Err              error
}

// GetModelRAMEstimate resolves RAM-estimation numbers for either an
// online catalog entry (catalogID, "name:tag" or bare family) or a
// model in the local inventory (runtime + modelID). Estimate failures
// come back in Err with SystemRAMBytes still populated; transport
// failures also land in Err.
func (c *Client) GetModelRAMEstimate(ctx context.Context, catalogID, runtime, modelID string) ModelRAMEstimate {
	resp, err := c.agent.GetModelRAMEstimate(ctx, &proto.GetModelRAMEstimateRequest{
		CatalogId: catalogID,
		Runtime:   runtime,
		ModelId:   modelID,
	})
	if err != nil {
		return ModelRAMEstimate{Err: err}
	}
	out := ModelRAMEstimate{
		WeightsBytes:     resp.GetWeightsBytes(),
		KVBytesPerToken:  resp.GetKvBytesPerToken(),
		MaxContextTokens: resp.GetMaxContextTokens(),
		Architecture:     resp.GetArchitecture(),
		SystemRAMBytes:   resp.GetSystemRamBytes(),
	}
	if s := resp.GetError(); s != "" {
		out.Err = fmt.Errorf("%s", s)
	}
	return out
}

// InstallOpenRuntime opens the InstallOpenRuntime streaming RPC for the
// given runtime and returns a channel of progress frames. The channel closes
// after the terminal Done=true frame (or after a stream error). Cancelling
// ctx kills the install subprocess server-side.
func (c *Client) InstallOpenRuntime(ctx context.Context, runtime string) (<-chan InstallProgress, error) {
	stream, err := c.agent.InstallOpenRuntime(ctx, &proto.InstallOpenRuntimeRequest{Runtime: runtime})
	if err != nil {
		return nil, err
	}
	out := make(chan InstallProgress, 8)
	go func() {
		defer close(out)
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				out <- InstallProgress{Err: err}
				return
			}
			out <- InstallProgress{
				Line:  frame.GetLine(),
				Done:  frame.GetDone(),
				Ok:    frame.GetOk(),
				Error: frame.GetError(),
			}
			if frame.GetDone() {
				return
			}
		}
	}()
	return out, nil
}

// UpdateConfig sends a runtime config patch. Returns the agent's confirmation
// summary line (e.g. "updated: [local_model=qwen3-coder, cloud=anthropic/...]").
func (c *Client) UpdateConfig(ctx context.Context, u ConfigUpdate) (string, error) {
	resp, err := c.agent.UpdateConfig(ctx, &proto.UpdateConfigRequest{
		OllamaUrl:                 u.OllamaURL,
		OpenRuntime:               u.OpenRuntime,
		OpenModel:                 u.OpenModel,
		OpenDefaultModel:          u.OpenDefaultModel,
		CloudProvider:             u.CloudProvider,
		CloudModel:                u.CloudModel,
		CloudApiKey:               u.CloudAPIKey,
		CloudBaseUrl:              u.CloudBaseURL,
		LocusMode:                 u.LocusMode,
		WatchdogEnabled:           u.WatchdogEnabled,
		WatchdogEcho:              u.WatchdogEcho,
		WatchdogMode:              u.WatchdogMode,
		WatchdogChecks:            u.WatchdogChecks,
		WatchdogEscalateAfter:     u.WatchdogEscalateAfter,
		ElideToolResults:          u.ElideToolResults,
		LossyToolElision:          u.LossyToolElision,
		RawRetentionDays:          u.RawRetentionDays,
		CompactedRetentionDays:    u.CompactedRetentionDays,
		KeepForever:               u.KeepForever,
		CompactionEnabled:         u.CompactionEnabled,
		ToolElisionOnly:           u.ToolElisionOnly,
		ToolLoopMaxIterations:     u.ToolLoopMaxIterations,
		ModelTierKey:              u.ModelTierKey,
		ModelTierValue:            u.ModelTierValue,
		MistralrsIsq:              u.MistralRSISQ,
		MistralrsPagedAttn:        u.MistralRSPagedAttn,
		MistralrsPaMemoryFraction: u.MistralRSPAMemoryFraction,
	})
	if err != nil {
		return "", err
	}
	if !resp.GetSuccess() {
		return "", fmt.Errorf("%s", resp.GetMessage())
	}
	return resp.GetMessage(), nil
}

func mapRuntimeModels(models []*proto.RuntimeModel) []RuntimeModel {
	out := make([]RuntimeModel, 0, len(models))
	for _, model := range models {
		out = append(out, mapRuntimeModel(model))
	}
	return out
}

func mapRuntimeModel(model *proto.RuntimeModel) RuntimeModel {
	if model == nil {
		return RuntimeModel{}
	}
	return RuntimeModel{
		ID:                 model.GetId(),
		DisplayName:        model.GetDisplayName(),
		Runtime:            model.GetRuntime(),
		Source:             model.GetSource(),
		Path:               model.GetPath(),
		Format:             model.GetFormat(),
		Family:             model.GetFamily(),
		Quantization:       model.GetQuantization(),
		SizeBytes:          model.GetSizeBytes(),
		ModifiedAt:         parseRuntimeTime(model.GetModifiedAt()),
		DownloadState:      model.GetDownloadState(),
		DownloadURL:        model.GetDownloadUrl(),
		DownloadedBytes:    model.GetDownloadedBytes(),
		DownloadTotalBytes: model.GetDownloadTotalBytes(),
		DownloadError:      model.GetDownloadError(),
		RuntimeState:       model.GetRuntimeState(),
		SupportsChat:       model.GetSupportsChat(),
		SupportsEmbed:      model.GetSupportsEmbed(),
		SupportsTools:      model.GetSupportsTools(),
		Active:             model.GetActive(),
		CatalogID:          model.GetCatalogId(),
		KVBytesPerToken:    model.GetKvBytesPerToken(),
		MaxContextTokens:   model.GetMaxContextTokens(),
	}
}

func mapRuntimeInstances(instances []*proto.RuntimeInstance) []RuntimeInstance {
	out := make([]RuntimeInstance, 0, len(instances))
	for _, instance := range instances {
		out = append(out, mapRuntimeInstance(instance))
	}
	return out
}

func mapRuntimeInstance(instance *proto.RuntimeInstance) RuntimeInstance {
	if instance == nil {
		return RuntimeInstance{}
	}
	return RuntimeInstance{
		ID:           instance.GetId(),
		Runtime:      instance.GetRuntime(),
		ModelID:      instance.GetModelId(),
		State:        instance.GetState(),
		PID:          int(instance.GetPid()),
		Address:      instance.GetAddress(),
		Port:         int(instance.GetPort()),
		Endpoint:     instance.GetEndpoint(),
		StartedAt:    parseRuntimeTime(instance.GetStartedAt()),
		ReadyAt:      parseRuntimeTime(instance.GetReadyAt()),
		RestartCount: int(instance.GetRestartCount()),
		LastExitCode: int(instance.GetLastExitCode()),
		LastError:    instance.GetLastError(),
		LogPath:      instance.GetLogPath(),
	}
}

func mapRuntimeEndpoints(endpoints []*proto.RuntimeEndpoint) []RuntimeEndpoint {
	out := make([]RuntimeEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, RuntimeEndpoint{
			ID:            endpoint.GetId(),
			Kind:          endpoint.GetKind(),
			DisplayName:   endpoint.GetDisplayName(),
			BaseURL:       endpoint.GetBaseUrl(),
			Scope:         endpoint.GetScope(),
			State:         endpoint.GetState(),
			ActiveRoles:   append([]string(nil), endpoint.GetActiveRoles()...),
			Models:        append([]string(nil), endpoint.GetModels()...),
			LastCheckedAt: parseRuntimeTime(endpoint.GetLastCheckedAt()),
			LatencyMS:     endpoint.GetLatencyMs(),
			LastError:     endpoint.GetLastError(),
			AuthState:     endpoint.GetAuthState(),
		})
	}
	return out
}

func mapRuntimeLogs(logs []*proto.RuntimeLogEntry) []RuntimeLogEntry {
	out := make([]RuntimeLogEntry, 0, len(logs))
	for _, entry := range logs {
		out = append(out, mapRuntimeLog(entry))
	}
	return out
}

func mapRuntimeLog(entry *proto.RuntimeLogEntry) RuntimeLogEntry {
	if entry == nil {
		return RuntimeLogEntry{}
	}
	return RuntimeLogEntry{
		Timestamp: parseRuntimeTime(entry.GetTimestamp()),
		Source:    entry.GetSource(),
		Level:     entry.GetLevel(),
		RuntimeID: entry.GetRuntimeId(),
		ModelID:   entry.GetModelId(),
		Message:   entry.GetMessage(),
	}
}

func parseRuntimeTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

// StreamMsg is a typed event produced by a streaming chat turn.
type StreamMsg struct {
	Type         StreamMsgType
	Token        string // for TypeToken
	Note         string // for TypeProgress
	Final        string // for TypeDone (full response)
	Notice       string // for TypeDone (agent informational note, e.g. cloud absent)
	Model        string // for TypeDone
	TokIn        int    // for TypeDone
	TokOut       int    // for TypeDone
	Err          error  // for TypeError
	ToolUseID    string // for TypeToolUseStart/Stop, TypeToolExecStart/Complete, TypePermissionRequired
	ToolName     string // for TypeToolUseStart, TypePermissionRequired
	ArgsSummary  string // for TypeToolUseStop
	ArgsJSON     string // for TypePermissionRequired
	Summary      string // for TypeToolExecComplete
	Detail       string // for TypeToolExecComplete (clean outcome token)
	StartLine    int    // for TypeToolExecComplete (1-based first line of an edit/write; 0 = n/a)
	IsError      bool   // for TypeToolExecComplete
	RouteModel   string // for TypeRouteSelected (engine handling the turn)
	RouteCloud   bool   // for TypeRouteSelected (true = cloud, false = local)
	Tier         string // for TypePermissionRequired ("W" | "X")
	Destructive  bool   // for TypePermissionRequired (display-only ⚠ hint)
	WatchdogKind string // for TypeWatchdog ("challenge" | "block" | "echo")
	Protocol     string // for TypeWatchdog (protocol name, empty for echo)
	Thread       string // for TypeWatchdog echo only ("watchdog" | "main")

	SubAgentID       string   // for TypeSubAgent
	SubAgentParentID string   // for TypeSubAgent
	SubAgentTitle    string   // for TypeSubAgent
	SubAgentKind     string   // for TypeSubAgent
	GrantedTools     []string // for TypeSubAgent
	IgnoredTools     []string // for TypeSubAgent
	SubAgentText     string   // for TypeSubAgent
	SubAgentToolID   string   // for TypeSubAgent
}

type StreamMsgType int

const (
	TypeToken StreamMsgType = iota
	TypeProgress
	TypeDone
	TypeError
	TypeToolUseStart
	TypeToolUseStop
	TypeToolExecStart
	TypeToolExecComplete
	TypePermissionRequired
	TypeRouteSelected
	TypeWatchdog
	TypeSubAgent
)

func toProtoImages(images []InlineImage) []*proto.InlineImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]*proto.InlineImage, 0, len(images))
	for _, img := range images {
		out = append(out, &proto.InlineImage{
			Index:     img.Index,
			Data:      img.Data,
			MediaType: img.MediaType,
		})
	}
	return out
}

// StreamChat opens a streaming chat call and emits typed messages on the
// returned channel. workDir is the active project root; the agent uses it
// to prepend the project's .cercano/context.md to the prompt for project
// awareness. images are user-attached images spliced in at "[image N]" markers.
// The channel closes when the stream ends.
func (c *Client) StreamChat(ctx context.Context, conversationID, input, workDir string, images ...InlineImage) (<-chan StreamMsg, error) {
	stream, err := c.agent.StreamProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:          input,
		ConversationId: conversationID,
		WorkDir:        workDir,
		Images:         toProtoImages(images),
	})
	if err != nil {
		return nil, fmt.Errorf("stream open: %w", err)
	}

	out := make(chan StreamMsg, 16)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				out <- StreamMsg{Type: TypeError, Err: err}
				return
			}
			if td := msg.GetTokenDelta(); td != nil {
				out <- StreamMsg{Type: TypeToken, Token: td.GetContent()}
				continue
			}
			if pg := msg.GetProgress(); pg != nil {
				out <- StreamMsg{Type: TypeProgress, Note: pg.GetMessage()}
				continue
			}
			if fr := msg.GetFinalResponse(); fr != nil {
				m := fr.GetRoutingMetadata()
				out <- StreamMsg{
					Type:   TypeDone,
					Final:  fr.GetOutput(),
					Notice: fr.GetNotice(),
					Model:  m.GetModelName(),
					TokIn:  int(fr.GetInputTokens()),
					TokOut: int(fr.GetOutputTokens()),
				}
				continue
			}
			if tus := msg.GetToolUseStart(); tus != nil {
				out <- StreamMsg{
					Type:      TypeToolUseStart,
					ToolUseID: tus.GetToolUseId(),
					ToolName:  tus.GetToolName(),
				}
				continue
			}
			if tup := msg.GetToolUseStop(); tup != nil {
				out <- StreamMsg{
					Type:        TypeToolUseStop,
					ToolUseID:   tup.GetToolUseId(),
					ArgsSummary: tup.GetArgsSummary(),
				}
				continue
			}
			if tes := msg.GetToolExecStart(); tes != nil {
				out <- StreamMsg{
					Type:      TypeToolExecStart,
					ToolUseID: tes.GetToolUseId(),
				}
				continue
			}
			if tec := msg.GetToolExecComplete(); tec != nil {
				out <- StreamMsg{
					Type:      TypeToolExecComplete,
					ToolUseID: tec.GetToolUseId(),
					Summary:   tec.GetSummary(),
					Detail:    tec.GetDetail(),
					StartLine: int(tec.GetStartLine()),
					IsError:   tec.GetIsError(),
				}
				continue
			}
			if pr := msg.GetPermissionRequired(); pr != nil {
				out <- StreamMsg{
					Type:        TypePermissionRequired,
					ToolUseID:   pr.GetToolUseId(),
					ToolName:    pr.GetToolName(),
					ArgsJSON:    pr.GetArgsJson(),
					Tier:        pr.GetTier(),
					Destructive: pr.GetDestructive(),
				}
				continue
			}
			if rs := msg.GetRouteSelected(); rs != nil {
				out <- StreamMsg{
					Type:       TypeRouteSelected,
					RouteModel: rs.GetModel(),
					RouteCloud: rs.GetIsCloud(),
				}
				continue
			}
			if we := msg.GetWatchdogEvent(); we != nil {
				out <- streamMsgFromWatchdogEvent(we)
				continue
			}
			if se := msg.GetSubAgentEvent(); se != nil {
				out <- StreamMsg{
					Type:             TypeSubAgent,
					SubAgentID:       se.GetId(),
					SubAgentParentID: se.GetParentId(),
					SubAgentTitle:    se.GetTitle(),
					SubAgentKind:     se.GetKind(),
					GrantedTools:     append([]string(nil), se.GetGrantedTools()...),
					IgnoredTools:     append([]string(nil), se.GetIgnoredTools()...),
					SubAgentText:     se.GetText(),
					SubAgentToolID:   se.GetToolUseId(),
					ToolUseID:        se.GetToolUseId(),
					ToolName:         se.GetToolName(),
					ArgsSummary:      se.GetArgsSummary(),
					Summary:          se.GetSummary(),
					Detail:           se.GetDetail(),
					StartLine:        int(se.GetStartLine()),
					IsError:          se.GetIsError(),
				}
				continue
			}
		}
	}()
	return out, nil
}

// streamMsgFromWatchdogEvent converts a proto WatchdogEvent into a StreamMsg.
// Summary carries proto.Text; Protocol and Thread carry their namesake fields.
func streamMsgFromWatchdogEvent(we *proto.WatchdogEvent) StreamMsg {
	return StreamMsg{
		Type:         TypeWatchdog,
		WatchdogKind: we.GetKind(),
		Protocol:     we.GetProtocol(),
		Summary:      we.GetText(),
		Thread:       we.GetThread(),
	}
}

// SetPermissionMode changes the agent's session permission mode.
func (c *Client) SetPermissionMode(ctx context.Context, mode string) error {
	res, err := c.agent.SetPermissionMode(ctx, &proto.SetPermissionModeRequest{Mode: mode})
	if err != nil {
		return err
	}
	if !res.GetOk() {
		return fmt.Errorf("%s", res.GetError())
	}
	return nil
}

// GetPermissionMode reads the agent's current session permission mode.
func (c *Client) GetPermissionMode(ctx context.Context) (string, error) {
	res, err := c.agent.GetPermissionMode(ctx, &proto.GetPermissionModeRequest{})
	if err != nil {
		return "", err
	}
	return res.GetMode(), nil
}

// AllowToolCall approves a paused tool call awaiting permission. conversationID
// scopes the reply so it reaches the waiter in the right conversation.
func (c *Client) AllowToolCall(ctx context.Context, conversationID, toolUseID string) error {
	_, err := c.agent.AllowToolCall(ctx, &proto.AllowToolCallRequest{ToolUseId: toolUseID, ConversationId: conversationID})
	return err
}

// DenyToolCall rejects a paused tool call awaiting permission. conversationID
// scopes the reply so it reaches the waiter in the right conversation.
func (c *Client) DenyToolCall(ctx context.Context, conversationID, toolUseID string) error {
	_, err := c.agent.DenyToolCall(ctx, &proto.DenyToolCallRequest{ToolUseId: toolUseID, ConversationId: conversationID})
	return err
}

// DenyToolCallWithMessage denies a paused tool call and delivers a "chat about
// this" redirect: the server records message as the tool_result and continues
// the same turn so the model responds to it inline (no fresh turn).
func (c *Client) DenyToolCallWithMessage(ctx context.Context, conversationID, toolUseID, message string) error {
	_, err := c.agent.DenyToolCall(ctx, &proto.DenyToolCallRequest{ToolUseId: toolUseID, ConversationId: conversationID, Message: message})
	return err
}

// ProviderCaps is the capability set reported by the active provider.
type ProviderCaps struct {
	SupportsTools         bool
	SupportsParallelTools bool
	SupportsCaching       bool
	SupportsVision        bool
	MaxToolsPerCall       int32
}

// GetProviderCapabilities reports what the active provider supports.
func (c *Client) GetProviderCapabilities(ctx context.Context) (ProviderCaps, error) {
	res, err := c.agent.GetProviderCapabilities(ctx, &proto.GetProviderCapabilitiesRequest{})
	if err != nil {
		return ProviderCaps{}, err
	}
	return ProviderCaps{
		SupportsTools:         res.GetSupportsTools(),
		SupportsParallelTools: res.GetSupportsParallelTools(),
		SupportsCaching:       res.GetSupportsCaching(),
		SupportsVision:        res.GetSupportsVision(),
		MaxToolsPerCall:       res.GetMaxToolsPerCall(),
	}, nil
}

// McpServer is a point-in-time view of one hosted MCP server.
type McpServer struct {
	Name      string
	State     string
	ToolCount int
	Err       string
}

// ListMcpServers returns a snapshot of all hosted MCP servers.
func (c *Client) ListMcpServers(ctx context.Context) ([]McpServer, error) {
	resp, err := c.agent.ListMcpServers(ctx, &proto.ListMcpServersRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]McpServer, 0, len(resp.GetServers()))
	for _, s := range resp.GetServers() {
		out = append(out, McpServer{Name: s.GetName(), State: s.GetState(),
			ToolCount: int(s.GetToolCount()), Err: s.GetError()})
	}
	return out, nil
}

// AddMcpServer connects a new MCP server by name and persists it to mcp.yaml.
func (c *Client) AddMcpServer(ctx context.Context, name, command string, args []string, env map[string]string) error {
	resp, err := c.agent.AddMcpServer(ctx, &proto.AddMcpServerRequest{
		Name: name, Command: command, Args: args, Env: env})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// RemoveMcpServer stops a hosted MCP server and removes it from mcp.yaml.
func (c *Client) RemoveMcpServer(ctx context.Context, name string) error {
	resp, err := c.agent.RemoveMcpServer(ctx, &proto.RemoveMcpServerRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// RestartMcpServer tears down and reconnects a hosted MCP server.
func (c *Client) RestartMcpServer(ctx context.Context, name string) error {
	resp, err := c.agent.RestartMcpServer(ctx, &proto.RestartMcpServerRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// CloudModelInfo is one entry in a cloud profile's model catalog.
type CloudModelInfo struct {
	ID          string
	DisplayName string // human-friendly label; may equal ID
}

// ListCloudProfileModels fetches the model catalog available to a specific
// cloud profile (through Meridian for meridian-routed profiles, or via a
// direct API-key request for others). Returns (models, "") on success. On
// failure the models slice is empty and the second return is a short
// human-readable reason so the caller can render a curated fallback list
// and surface why the live fetch didn't work.
func (c *Client) ListCloudProfileModels(ctx context.Context, profileName string) ([]CloudModelInfo, string, error) {
	resp, err := c.agent.ListCloudProfileModels(ctx, &proto.ListCloudProfileModelsRequest{ProfileName: profileName})
	if err != nil {
		return nil, "", err
	}
	out := make([]CloudModelInfo, 0, len(resp.GetModels()))
	for _, m := range resp.GetModels() {
		out = append(out, CloudModelInfo{ID: m.GetId(), DisplayName: m.GetDisplayName()})
	}
	return out, resp.GetError(), nil
}

// CloudProfileInfo is a point-in-time view of one cloud profile.
type CloudProfileInfo struct {
	Name    string
	Flavor  string
	BaseURL string
	Model   string
	HasKey  bool // a key exists in the keychain for this profile
	Backend string
	Route   string // "direct" (default), "meridian", "ccr" (future) — selects adapter-specific auth
}

// GetCloudProfiles returns all configured cloud profiles and the name of the
// currently active profile.
func (c *Client) GetCloudProfiles(ctx context.Context) ([]CloudProfileInfo, string, error) {
	resp, err := c.agent.GetCloudProfiles(ctx, &proto.GetCloudProfilesRequest{})
	if err != nil {
		return nil, "", err
	}
	out := make([]CloudProfileInfo, 0, len(resp.GetProfiles()))
	for _, p := range resp.GetProfiles() {
		out = append(out, CloudProfileInfo{
			Name:    p.GetName(),
			Flavor:  p.GetFlavor(),
			BaseURL: p.GetBaseUrl(),
			Model:   p.GetModel(),
			HasKey:  p.GetHasKey(),
			Backend: p.GetBackend(),
			Route:   p.GetRoute(),
		})
	}
	return out, resp.GetActive(), nil
}

// SetActiveCloudProfile switches the active cloud profile to the named one.
func (c *Client) SetActiveCloudProfile(ctx context.Context, name string) error {
	resp, err := c.agent.SetActiveCloudProfile(ctx, &proto.SetActiveCloudProfileRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// SetCloudProfileKey stores an API key for the named cloud profile.
func (c *Client) SetCloudProfileKey(ctx context.Context, name, key string) error {
	resp, err := c.agent.SetCloudProfileKey(ctx, &proto.SetCloudProfileKeyRequest{Name: name, ApiKey: key})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// UpsertCloudProfile creates or updates a cloud profile's metadata.
func (c *Client) UpsertCloudProfile(ctx context.Context, p CloudProfileInfo) error {
	resp, err := c.agent.UpsertCloudProfile(ctx, &proto.UpsertCloudProfileRequest{
		Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseUrl: p.BaseURL, Model: p.Model, Route: p.Route,
	})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// RemoveCloudProfile deletes a cloud profile and its keychain key.
func (c *Client) RemoveCloudProfile(ctx context.Context, name string) error {
	resp, err := c.agent.RemoveCloudProfile(ctx, &proto.RemoveCloudProfileRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// AllowToolCallPersist approves a paused tool call and, when persist is true,
// asks the agent to allowlist it for silent future runs.
func (c *Client) AllowToolCallPersist(ctx context.Context, conversationID, toolUseID string, persist bool) error {
	_, err := c.agent.AllowToolCall(ctx, &proto.AllowToolCallRequest{ConversationId: conversationID, ToolUseId: toolUseID, Persist: persist})
	return err
}

// splitChecks splits a comma-separated watchdog checks string into a slice.
// Empty or whitespace-only input returns nil (no checks configured).
func splitChecks(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// atoiOr parses s as an integer, returning def on failure.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// ChatGPTLoginMsg is one frame of the ChatGPT sign-in stream. The first
// message carries VerificationURL + UserCode to display; the terminal message
// has Done=true with Ok/Error and (on success) the created ProfileName and
// AccountID. Err is set only on transport failure and is terminal.
type ChatGPTLoginMsg struct {
	VerificationURL string
	UserCode        string
	Done            bool
	Ok              bool
	Error           string
	ProfileName     string
	AccountID       string
	Err             error
}

// StartChatGPTLogin opens the ChatGPT subscription sign-in stream and returns
// a channel of frames. The caller shows the first frame's code + URL, then
// waits for the terminal (Done) frame. Cancel ctx to abort the sign-in.
func (c *Client) StartChatGPTLogin(ctx context.Context, profileName, model string, setActive bool) (<-chan ChatGPTLoginMsg, error) {
	stream, err := c.agent.StartChatGPTLogin(ctx, &proto.StartChatGPTLoginRequest{
		ProfileName: profileName,
		Model:       model,
		SetActive:   setActive,
	})
	if err != nil {
		return nil, err
	}
	out := make(chan ChatGPTLoginMsg, 8)
	go func() {
		defer close(out)
		for {
			ev, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				out <- ChatGPTLoginMsg{Err: err}
				return
			}
			out <- ChatGPTLoginMsg{
				VerificationURL: ev.GetVerificationUrl(),
				UserCode:        ev.GetUserCode(),
				Done:            ev.GetDone(),
				Ok:              ev.GetOk(),
				Error:           ev.GetError(),
				ProfileName:     ev.GetProfileName(),
				AccountID:       ev.GetAccountId(),
			}
		}
	}()
	return out, nil
}

// ClaudeLoginMsg is one frame of the Claude subscription sign-in stream. The
// first message carries AuthorizeURL to open in a browser; the terminal
// message has Done=true with Ok/Error and (on success) the created
// ProfileName. Err is set only on transport failure and is terminal.
type ClaudeLoginMsg struct {
	AuthorizeURL string
	Done         bool
	Ok           bool
	Error        string
	ProfileName  string
	Err          error
}

// StartClaudeLogin opens the Claude subscription sign-in stream and returns a
// channel of frames. The caller opens the first frame's authorize URL in a
// browser, then waits for the terminal (Done) frame. Cancel ctx to abort the
// sign-in.
func (c *Client) StartClaudeLogin(ctx context.Context, profileName, model string, setActive bool) (<-chan ClaudeLoginMsg, error) {
	stream, err := c.agent.StartClaudeLogin(ctx, &proto.StartClaudeLoginRequest{
		ProfileName: profileName,
		Model:       model,
		SetActive:   setActive,
	})
	if err != nil {
		return nil, err
	}
	out := make(chan ClaudeLoginMsg, 8)
	go func() {
		defer close(out)
		for {
			ev, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				out <- ClaudeLoginMsg{Err: err}
				return
			}
			out <- ClaudeLoginMsg{
				AuthorizeURL: ev.GetAuthorizeUrl(),
				Done:         ev.GetDone(),
				Ok:           ev.GetOk(),
				Error:        ev.GetError(),
				ProfileName:  ev.GetProfileName(),
			}
		}
	}()
	return out, nil
}
