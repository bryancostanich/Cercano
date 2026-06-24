package localruntime

import (
	"context"
	"time"
)

const (
	StateUnknown     = "unknown"
	StateHealthy     = "healthy"
	StateDegraded    = "degraded"
	StateUnreachable = "unreachable"
	StateStopped     = "stopped"
	StateStarting    = "starting"
	StateRunning     = "running"
	StateUnhealthy   = "unhealthy"
	StateCrashed     = "crashed"
	StateFailed      = "failed"
)

// Manager owns local runtime inventory, process state, endpoint state, and logs.
type Manager interface {
	RegisterProvider(Provider)
	Providers() []ProviderInfo
	Inventory(context.Context) ([]ModelRecord, error)
	Instances(context.Context) ([]InstanceRecord, error)
	Endpoints(context.Context) ([]EndpointRecord, error)
	SetEndpoints([]EndpointRecord)
	UpdateInstance(InstanceRecord)
	Start(context.Context, StartRequest) (*InstanceRecord, error)
	Stop(context.Context, StopRequest) error
	Restart(context.Context, RestartRequest) (*InstanceRecord, error)
	DownloadModel(context.Context, DownloadRequest) (*ModelRecord, error)
	CancelDownload(context.Context, DownloadRequest) (*ModelRecord, error)
	DeleteModel(context.Context, DeleteModelRequest) error
	Status(context.Context) (*StatusSnapshot, error)
	Logs(context.Context, LogRequest) ([]LogEntry, error)
	WriteLog(LogEntry)
}

// Provider is implemented by a concrete runtime backend such as llama-server.
type Provider interface {
	Name() string
	Capabilities() RuntimeCapabilities
	Discover(context.Context) ([]ModelRecord, error)
	Start(context.Context, StartRequest, LogSink) (*InstanceRecord, error)
	Stop(context.Context, string) error
	Probe(context.Context, string) (*InstanceHealth, error)
}

// LogSink accepts runtime logs from providers and supervisors.
type LogSink interface {
	WriteLog(LogEntry)
}

type RuntimeCapabilities struct {
	ManagedProcesses bool
	CanStart         bool
	CanStop          bool
	CanRestart       bool
	CanListModels    bool
	CanStreamLogs    bool
	SupportsChat     bool
	SupportsEmbed    bool
	SupportsTools    bool
}

type ProviderInfo struct {
	Name         string
	Capabilities RuntimeCapabilities
}

type ModelRecord struct {
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

type InstanceRecord struct {
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

type EndpointRecord struct {
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

type InstanceHealth struct {
	InstanceID string
	State      string
	LatencyMS  int64
	Error      string
	CheckedAt  time.Time
}

type LogEntry struct {
	Timestamp time.Time
	Source    string
	Level     string
	RuntimeID string
	ModelID   string
	Message   string
}

type StartRequest struct {
	Runtime string
	ModelID string
}

type StopRequest struct {
	InstanceID string
}

type RestartRequest struct {
	InstanceID string
	Runtime    string
	ModelID    string
}

type DownloadRequest struct {
	Runtime string
	ModelID string
}

type DeleteModelRequest struct {
	Runtime string
	ModelID string
}

type LogRequest struct {
	Tail   int
	Source string
	Since  time.Time
}

type StatusSnapshot struct {
	Models    []ModelRecord
	Instances []InstanceRecord
	Endpoints []EndpointRecord
	Logs      []LogEntry
}
