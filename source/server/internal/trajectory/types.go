package trajectory

type Trajectory struct {
	SchemaVersion string         `json:"schema_version"`
	SessionID     string         `json:"session_id,omitempty"`
	TrajectoryID  string         `json:"trajectory_id,omitempty"`
	Agent         Agent          `json:"agent"`
	Steps         []Step         `json:"steps"`
	Notes         string         `json:"notes,omitempty"`
	FinalMetrics  *FinalMetrics  `json:"final_metrics,omitempty"`
	Extra         map[string]any `json:"extra,omitempty"`
}
type Agent struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	ModelName       string         `json:"model_name,omitempty"`
	ToolDefinitions []any          `json:"tool_definitions,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
}
type Step struct {
	StepID           int            `json:"step_id"`
	Timestamp        string         `json:"timestamp,omitempty"`
	Source           string         `json:"source"`
	ModelName        string         `json:"model_name,omitempty"`
	Message          string         `json:"message"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	Observation      *Observation   `json:"observation,omitempty"`
	Metrics          *Metrics       `json:"metrics,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
	LLMCallCount     *int           `json:"llm_call_count,omitempty"`
	IsCopiedContext  *bool          `json:"is_copied_context,omitempty"`
}
type ToolCall struct {
	ToolCallID   string         `json:"tool_call_id"`
	FunctionName string         `json:"function_name"`
	Arguments    map[string]any `json:"arguments"`
	Extra        map[string]any `json:"extra,omitempty"`
}
type Observation struct {
	Results []ObservationResult `json:"results"`
}
type ObservationResult struct {
	SourceCallID          string                  `json:"source_call_id,omitempty"`
	Content               string                  `json:"content,omitempty"`
	SubagentTrajectoryRef []SubagentTrajectoryRef `json:"subagent_trajectory_ref,omitempty"`
	Extra                 map[string]any          `json:"extra,omitempty"`
}
type SubagentTrajectoryRef struct {
	TrajectoryID   string `json:"trajectory_id,omitempty"`
	TrajectoryPath string `json:"trajectory_path,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
}
type Metrics struct {
	PromptTokens     *int           `json:"prompt_tokens,omitempty"`
	CompletionTokens *int           `json:"completion_tokens,omitempty"`
	CachedTokens     *int           `json:"cached_tokens,omitempty"`
	CostUSD          *float64       `json:"cost_usd,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}
type FinalMetrics struct {
	TotalPromptTokens     *int           `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens *int           `json:"total_completion_tokens,omitempty"`
	TotalCachedTokens     *int           `json:"total_cached_tokens,omitempty"`
	TotalCostUSD          *float64       `json:"total_cost_usd,omitempty"`
	TotalSteps            int            `json:"total_steps,omitempty"`
	Extra                 map[string]any `json:"extra,omitempty"`
}

type Manifest struct {
	Format         string          `json:"format"`
	FormatVersion  int             `json:"format_version"`
	CreatedAt      string          `json:"created_at"`
	RootTrajectory string          `json:"root_trajectory"`
	SchemaVersion  string          `json:"schema_version"`
	ConversationID string          `json:"conversation_id"`
	BundleName     string          `json:"bundle_name"`
	Redaction      Redaction       `json:"redaction"`
	Artifacts      []Artifact      `json:"artifacts,omitempty"`
	Subagents      []SubagentEntry `json:"subagents,omitempty"`
}
type Redaction struct {
	Mode    string `json:"mode"`
	Warning string `json:"warning,omitempty"`
}
type Artifact struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	StepID     int    `json:"step_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}
type SubagentEntry struct {
	TrajectoryID string `json:"trajectory_id"`
	Path         string `json:"path"`
}
