package localruntime

import (
	"context"
	"time"
)

// The download and instance lifecycle state machines live in state.go
// (DownloadState, InstanceState) with their legal-transition tables. Endpoint
// health uses EndpointStateUnknown and plain strings, a separate concern.

// Manager owns local runtime inventory, process state, endpoint state, and logs.
type Manager interface {
	RegisterProvider(Provider)
	// RegisterObserver adds an Observer notified on every download/instance
	// state transition. Wire observers at startup, before state mutates.
	RegisterObserver(Observer)
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
	// EnsureModelsPresent resolves the given model refs for a runtime against
	// discovered inventory and enqueues a download for any not already present.
	// Engine-agnostic and idempotent — safe to call on every runtime switch.
	EnsureModelsPresent(ctx context.Context, runtime string, want []string) error
	// ResolveOpenModel resolves a wanted model ref to its canonical present
	// record for a runtime. Returns ErrModelNotFound (no match) or
	// ErrModelNotPresent (matched but not on disk, record still returned) so
	// callers can ensure/await/degrade instead of hard-failing at the engine.
	ResolveOpenModel(ctx context.Context, runtime, want string) (ModelRecord, error)
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
	// CatalogFormats is the ordered set of model formats this runtime browses
	// online, primary first (e.g. ["gguf"] for llama-server,
	// ["safetensors","uqff","gguf"] for mistral.rs). The server passes the
	// primary into the catalog's ListOptions.Format so browse surfaces models
	// the active runtime can actually load. Empty defaults to gguf.
	CatalogFormats []string
}

type ProviderInfo struct {
	Name         string
	Capabilities RuntimeCapabilities
}

type ModelRecord struct {
	ID          string
	DisplayName string
	Runtime     string
	Source      string
	Path        string
	// LoadTarget is what the runtime is pointed at when it differs from Path:
	// empty for a single-file GGUF (the runtime loads Path, the file); the
	// model's directory for a multi-file safetensors/UQFF model (mistral.rs is
	// launched with `-m <dir>`), where Path anchors the download inside that
	// directory. Empty means "use Path".
	LoadTarget    string
	Format        string
	Family        string
	Quantization  string
	SizeBytes     int64
	ModifiedAt    time.Time
	DownloadState DownloadState
	DownloadURL   string
	// DownloadURLs holds the ordered shard URLs for a multi-file model
	// (e.g. a split GGUF like GLM-4.5-Air's two-part Q4_K_M). When set, the
	// download manager fetches each URL into the model's directory (filename
	// taken from the URL) and the model counts as downloaded only once every
	// shard is present. Path names the first shard — what llama-server is
	// pointed at. Empty means a single-file model fetched from DownloadURL.
	DownloadURLs       []string
	DownloadedBytes    int64
	DownloadTotalBytes int64
	DownloadError      string
	RuntimeState       InstanceState
	SupportsChat       bool
	SupportsEmbed      bool
	SupportsTools      bool
	// SupportsVision reports whether the resolved model can accept image input
	// (a vision model with its mmproj present). Carried from the catalog through
	// to the provider's client capability, so a text-only model has images
	// stripped rather than sent to a backend that would reject them.
	SupportsVision bool
	// MmprojPath is the on-disk path to the multimodal projector GGUF for a
	// vision model, passed to llama-server as --mmproj. Empty for non-vision
	// models (and for a vision model whose projector file is not yet present,
	// which downgrades it to text-only until the file lands).
	MmprojPath string
	Active     bool
	// ExtraArgs are per-model runtime launch flags carried from the catalog
	// (CuratedModel.ExtraArgs) through to the provider's launch args. Empty for
	// models with no special launch requirements. The llama-server provider
	// appends these after the global config ExtraArgs.
	ExtraArgs []string
	// ContextSize is an optional profile-level launch-policy override for runtimes
	// that support configurable context windows. Zero means no profile override.
	ContextSize int
}

type InstanceRecord struct {
	ID           string
	Runtime      string
	ModelID      string
	State        InstanceState
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
	State      InstanceState
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
