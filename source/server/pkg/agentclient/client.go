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
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cercano/source/server/pkg/proto"
)

// Client owns the gRPC connection and exposes a high-level streaming API.
type Client struct {
	conn         *grpc.ClientConn
	agent        proto.AgentClient
	AutoLaunched bool   // true if Dial spawned a new cercano process
	ServerLog    string // path to the auto-launched server's log file, if any
}

// Dial connects to a cercano agent. If no listener exists at addr, auto-launches
// `cercano` (the agent binary) in the background and waits for it to come up.
// The spawned server outlives the CLI so VS Code / Zed / other clients can share it.
func Dial(ctx context.Context, addr string) (*Client, error) {
	// First try: short connect against an existing server.
	if c, err := connect(ctx, addr, 600*time.Millisecond); err == nil {
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
	return c, nil
}

func connect(ctx context.Context, addr string, timeout time.Duration) (*Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from CLI's tty/process group
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
		candidate := filepath.Join(filepath.Dir(exe), "cercano")
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

// Close releases the gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Config is the current runtime config reported by the agent.
type Config struct {
	OllamaURL      string
	LocalRuntime   string
	LocalModel     string
	EmbeddingModel string
	CloudProvider  string
	CloudModel     string
	CloudBaseURL   string
	CloudAPIKeySet bool
	CloudState     string // "ok" | "absent" | "error"
	Port           string
}

// GetConfig fetches the agent's current runtime config.
func (c *Client) GetConfig(ctx context.Context) (*Config, error) {
	resp, err := c.agent.GetConfig(ctx, &proto.GetConfigRequest{})
	if err != nil {
		return nil, err
	}
	return &Config{
		OllamaURL:      resp.GetOllamaUrl(),
		LocalRuntime:   resp.GetLocalRuntime(),
		LocalModel:     resp.GetLocalModel(),
		EmbeddingModel: resp.GetEmbeddingModel(),
		CloudProvider:  resp.GetCloudProvider(),
		CloudModel:     resp.GetCloudModel(),
		CloudBaseURL:   resp.GetCloudBaseUrl(),
		CloudAPIKeySet: resp.GetCloudApiKeySet(),
		CloudState:     resp.GetCloudState(),
		Port:           resp.GetPort(),
	}, nil
}

// ConfigUpdate is a sparse patch sent to UpdateConfig — only non-empty fields
// are applied. Use SetCloudAPIKey to explicitly send an empty key when the
// proxy handles auth.
type ConfigUpdate struct {
	OllamaURL     string
	LocalRuntime  string
	LocalModel    string
	CloudProvider string
	CloudModel    string
	CloudAPIKey   string
	CloudBaseURL  string
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
	Active             bool
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
	}, nil
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

func (c *Client) ListRuntimeModels(ctx context.Context) ([]RuntimeModel, error) {
	resp, err := c.agent.ListRuntimeModels(ctx, &proto.ListRuntimeModelsRequest{})
	if err != nil {
		return nil, err
	}
	return mapRuntimeModels(resp.GetModels()), nil
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

func (c *Client) DownloadRuntimeModel(ctx context.Context, runtimeName, modelID string) (*RuntimeModel, error) {
	resp, err := c.agent.DownloadRuntimeModel(ctx, &proto.DownloadRuntimeModelRequest{
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

// UpdateConfig sends a runtime config patch. Returns the agent's confirmation
// summary line (e.g. "updated: [local_model=qwen3-coder, cloud=anthropic/...]").
func (c *Client) UpdateConfig(ctx context.Context, u ConfigUpdate) (string, error) {
	resp, err := c.agent.UpdateConfig(ctx, &proto.UpdateConfigRequest{
		OllamaUrl:     u.OllamaURL,
		LocalRuntime:  u.LocalRuntime,
		LocalModel:    u.LocalModel,
		CloudProvider: u.CloudProvider,
		CloudModel:    u.CloudModel,
		CloudApiKey:   u.CloudAPIKey,
		CloudBaseUrl:  u.CloudBaseURL,
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
	Type        StreamMsgType
	Token       string // for TypeToken
	Note        string // for TypeProgress
	Final       string // for TypeDone (full response)
	Notice      string // for TypeDone (agent informational note, e.g. cloud absent)
	Model       string // for TypeDone
	TokIn       int    // for TypeDone
	TokOut      int    // for TypeDone
	Err         error  // for TypeError
	ToolUseID   string // for TypeToolUseStart/Stop, TypeToolExecStart/Complete, TypePermissionRequired
	ToolName    string // for TypeToolUseStart, TypePermissionRequired
	ArgsSummary string // for TypeToolUseStop
	ArgsJSON    string // for TypePermissionRequired
	Summary     string // for TypeToolExecComplete
	Detail      string // for TypeToolExecComplete (clean outcome token)
	IsError     bool   // for TypeToolExecComplete
	RouteModel  string // for TypeRouteSelected (engine handling the turn)
	RouteCloud  bool   // for TypeRouteSelected (true = cloud, false = local)
	Tier        string // for TypePermissionRequired ("W" | "X")
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
)

// StreamChat opens a streaming chat call and emits typed messages on the
// returned channel. workDir is the active project root; the agent uses it
// to prepend the project's .cercano/context.md to the prompt for project
// awareness. The channel closes when the stream ends.
func (c *Client) StreamChat(ctx context.Context, conversationID, input, workDir string) (<-chan StreamMsg, error) {
	stream, err := c.agent.StreamProcessRequest(ctx, &proto.ProcessRequestRequest{
		Input:          input,
		ConversationId: conversationID,
		WorkDir:        workDir,
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
					IsError:   tec.GetIsError(),
				}
				continue
			}
			if pr := msg.GetPermissionRequired(); pr != nil {
				out <- StreamMsg{
					Type:      TypePermissionRequired,
					ToolUseID: pr.GetToolUseId(),
					ToolName:  pr.GetToolName(),
					ArgsJSON:  pr.GetArgsJson(),
					Tier:      pr.GetTier(),
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
		}
	}()
	return out, nil
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

// AllowToolCall approves a paused tool call awaiting permission.
func (c *Client) AllowToolCall(ctx context.Context, toolUseID string) error {
	_, err := c.agent.AllowToolCall(ctx, &proto.AllowToolCallRequest{ToolUseId: toolUseID})
	return err
}

// DenyToolCall rejects a paused tool call awaiting permission.
func (c *Client) DenyToolCall(ctx context.Context, toolUseID string) error {
	_, err := c.agent.DenyToolCall(ctx, &proto.DenyToolCallRequest{ToolUseId: toolUseID})
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
