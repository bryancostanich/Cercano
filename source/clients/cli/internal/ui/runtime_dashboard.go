package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

const (
	runtimeActionStart   = "start"
	runtimeActionStop    = "stop"
	runtimeActionRestart = "restart"
	runtimeActionSep     = "\x1f"

	maxDashboardEndpoints = 8
	maxDashboardModels    = 12
	maxDashboardInstances = 8
	maxDashboardLogs      = 10
)

// runtimeDashboard wraps overlay.RowList with runtime status rows and
// start/stop/restart actions backed by the Cercano server.
type runtimeDashboard struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	list          overlay.RowList
}

type runtimeDashboardSnapshot struct {
	Config    *agentclient.Config
	ConfigErr error
	Status    *agentclient.RuntimeStatus
	StatusErr error
}

type runtimeDashboardAction struct {
	Kind       string
	Runtime    string
	ModelID    string
	InstanceID string
}

type runtimeDashboardActionMsg struct {
	Status string
}

func newRuntimeDashboard(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) (runtimeDashboard, tea.Cmd) {
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			action, err := parseRuntimeDashboardAction(row.Key)
			if err != nil {
				return "action failed: " + err.Error(), false, nil
			}
			return runtimeDashboardPendingStatus(action), false, runtimeDashboardActionCmd(ag, action)
		},
		OnReload: func() []overlay.Row {
			return buildRuntimeRows(ag)
		},
	}
	list := overlay.New("model runtime dashboard", buildRuntimeRows(ag), hooks)
	list.SetStatus("enter action rows; esc closes")
	return runtimeDashboard{
		palette: p,
		styles:  s,
		agent:   ag,
		width:   w,
		height:  h,
		list:    list,
	}, nil
}

func (d runtimeDashboard) Update(msg tea.KeyPressMsg) (runtimeDashboard, tea.Cmd, bool) {
	next, cmd, closed := d.list.Update(msg, d.styles)
	d.list = next
	return d, cmd, closed
}

func (d runtimeDashboard) View() string {
	return d.list.View(d.width, d.palette, d.styles)
}

func (d runtimeDashboard) setSize(w, h int) runtimeDashboard {
	d.width = w
	d.height = h
	return d
}

func (d runtimeDashboard) applyActionMsg(msg runtimeDashboardActionMsg) runtimeDashboard {
	d.list.Reload()
	d.list.SetStatus(msg.Status)
	return d
}

func buildRuntimeRows(ag *agentclient.Client) []overlay.Row {
	if ag == nil {
		return []overlay.Row{{Label: "runtime", Value: "agent client unavailable", ReadOnly: true}}
	}
	return runtimeRowsFromSnapshot(loadRuntimeDashboardSnapshot(ag))
}

func loadRuntimeDashboardSnapshot(ag *agentclient.Client) runtimeDashboardSnapshot {
	var snap runtimeDashboardSnapshot

	cfgCtx, cfgCancel := context.WithTimeout(context.Background(), 3*time.Second)
	snap.Config, snap.ConfigErr = ag.GetConfig(cfgCtx)
	cfgCancel()

	statusCtx, statusCancel := context.WithTimeout(context.Background(), 3*time.Second)
	snap.Status, snap.StatusErr = ag.GetRuntimeStatus(statusCtx)
	statusCancel()

	return snap
}

func runtimeRowsFromSnapshot(s runtimeDashboardSnapshot) []overlay.Row {
	rows := make([]overlay.Row, 0, 48)
	appendRuntimeConfigRows(&rows, s)

	if s.StatusErr != nil {
		rows = append(rows, overlay.Row{
			Label:    "runtime status",
			Value:    s.StatusErr.Error(),
			Hint:     "error",
			ReadOnly: true,
		})
		return rows
	}
	status := s.Status
	if status == nil {
		status = &agentclient.RuntimeStatus{}
	}

	appendRuntimeEndpointRows(&rows, status.Endpoints)
	appendRuntimeModelRows(&rows, status.Models, status.Instances)
	appendRuntimeInstanceRows(&rows, status.Instances)
	appendRuntimeLogRows(&rows, status.Logs)

	if len(rows) == 0 {
		rows = append(rows, overlay.Row{Label: "runtime", Value: "no runtime data", ReadOnly: true})
	}
	return rows
}

func appendRuntimeConfigRows(rows *[]overlay.Row, s runtimeDashboardSnapshot) {
	if s.ConfigErr != nil {
		*rows = append(*rows, overlay.Row{
			Label:    "config",
			Value:    s.ConfigErr.Error(),
			Hint:     "error",
			ReadOnly: true,
		})
		return
	}
	if s.Config == nil {
		return
	}
	cfg := s.Config
	*rows = append(*rows,
		overlay.Row{Label: "local runtime", Value: cfg.LocalRuntime, Hint: "active", ReadOnly: true},
		overlay.Row{Label: "local model", Value: cfg.LocalModel, ReadOnly: true},
		overlay.Row{Label: "embedding model", Value: cfg.EmbeddingModel, ReadOnly: true},
	)
	if cfg.CloudProvider != "" || cfg.CloudModel != "" || cfg.CloudBaseURL != "" {
		parts := nonEmptyParts(cfg.CloudProvider, cfg.CloudModel, cfg.CloudBaseURL)
		*rows = append(*rows, overlay.Row{
			Label:    "cloud endpoint",
			Value:    strings.Join(parts, " | "),
			Hint:     "configured",
			ReadOnly: true,
		})
	}
}

func appendRuntimeEndpointRows(rows *[]overlay.Row, endpoints []agentclient.RuntimeEndpoint) {
	*rows = append(*rows, overlay.Row{
		Label:    "external endpoints",
		Value:    countLabel(len(endpoints), "endpoint"),
		ReadOnly: true,
	})
	if len(endpoints) == 0 {
		*rows = append(*rows, overlay.Row{Label: "endpoint", Value: "none configured", ReadOnly: true})
		return
	}
	for i, endpoint := range endpoints {
		if i >= maxDashboardEndpoints {
			appendMoreRow(rows, "endpoints", len(endpoints)-i)
			break
		}
		*rows = append(*rows, overlay.Row{
			Label:    endpointLabel(endpoint),
			Value:    endpointValue(endpoint),
			Hint:     endpointHint(endpoint),
			ReadOnly: true,
		})
	}
}

func appendRuntimeModelRows(rows *[]overlay.Row, models []agentclient.RuntimeModel, instances []agentclient.RuntimeInstance) {
	*rows = append(*rows, overlay.Row{
		Label:    "downloaded models",
		Value:    countLabel(len(models), "model"),
		ReadOnly: true,
	})
	if len(models) == 0 {
		*rows = append(*rows, overlay.Row{Label: "model", Value: "no downloaded GGUF models found", ReadOnly: true})
		return
	}
	active := activeInstancesByModel(instances)
	for i, model := range models {
		if i >= maxDashboardModels {
			appendMoreRow(rows, "models", len(models)-i)
			break
		}
		running := active[runtimeModelKey(model.Runtime, model.ID)]
		hint := modelHint(model, running)
		*rows = append(*rows, overlay.Row{
			Label:    modelLabel(model),
			Value:    modelValue(model, running),
			Hint:     hint,
			ReadOnly: true,
		})
		if canStartRuntimeModel(model, running) {
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionStart, Runtime: model.Runtime, ModelID: model.ID}),
				Label: "start " + shortModelName(model.ID),
				Value: model.Runtime,
				Hint:  "enter",
			})
		}
	}
}

func appendRuntimeInstanceRows(rows *[]overlay.Row, instances []agentclient.RuntimeInstance) {
	*rows = append(*rows, overlay.Row{
		Label:    "runtime processes",
		Value:    countLabel(len(instances), "process"),
		ReadOnly: true,
	})
	if len(instances) == 0 {
		*rows = append(*rows, overlay.Row{Label: "process", Value: "none running", ReadOnly: true})
		return
	}
	for i, instance := range instances {
		if i >= maxDashboardInstances {
			appendMoreRow(rows, "processes", len(instances)-i)
			break
		}
		*rows = append(*rows, overlay.Row{
			Label:    instanceLabel(instance),
			Value:    instanceValue(instance),
			Hint:     instanceHint(instance),
			ReadOnly: true,
		})
		if instance.ID == "" {
			continue
		}
		if instance.State != "stopped" {
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionStop, InstanceID: instance.ID}),
				Label: "stop " + shortModelName(instance.ModelID),
				Value: instance.Runtime,
				Hint:  "enter",
			})
		}
		if instance.Runtime != "" && instance.ModelID != "" {
			*rows = append(*rows, overlay.Row{
				Key: encodeRuntimeDashboardAction(runtimeDashboardAction{
					Kind:       runtimeActionRestart,
					Runtime:    instance.Runtime,
					ModelID:    instance.ModelID,
					InstanceID: instance.ID,
				}),
				Label: "restart " + shortModelName(instance.ModelID),
				Value: instance.Runtime,
				Hint:  "enter",
			})
		}
	}
}

func appendRuntimeLogRows(rows *[]overlay.Row, logs []agentclient.RuntimeLogEntry) {
	*rows = append(*rows, overlay.Row{
		Label:    "recent logs",
		Value:    countLabel(len(logs), "entry"),
		ReadOnly: true,
	})
	if len(logs) == 0 {
		*rows = append(*rows, overlay.Row{Label: "log", Value: "no runtime logs yet", ReadOnly: true})
		return
	}
	start := 0
	if len(logs) > maxDashboardLogs {
		start = len(logs) - maxDashboardLogs
	}
	for _, entry := range logs[start:] {
		*rows = append(*rows, overlay.Row{
			Label:    logLabel(entry),
			Value:    shorten(entry.Message, 96),
			Hint:     logHint(entry),
			ReadOnly: true,
		})
	}
}

func runtimeDashboardActionCmd(ag *agentclient.Client, action runtimeDashboardAction) tea.Cmd {
	return func() tea.Msg {
		if ag == nil {
			return runtimeDashboardActionMsg{Status: "action failed: agent client unavailable"}
		}
		switch action.Kind {
		case runtimeActionStart:
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			instance, err := ag.StartRuntimeModel(ctx, action.Runtime, action.ModelID)
			if err != nil {
				return runtimeDashboardActionMsg{Status: "start failed: " + err.Error()}
			}
			return runtimeDashboardActionMsg{Status: "started " + shortModelName(instance.ModelID)}
		case runtimeActionStop:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := ag.StopRuntimeModel(ctx, action.InstanceID); err != nil {
				return runtimeDashboardActionMsg{Status: "stop failed: " + err.Error()}
			}
			return runtimeDashboardActionMsg{Status: "stopped " + shortID(action.InstanceID)}
		case runtimeActionRestart:
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			instance, err := ag.RestartRuntime(ctx, action.InstanceID, action.Runtime, action.ModelID)
			if err != nil {
				return runtimeDashboardActionMsg{Status: "restart failed: " + err.Error()}
			}
			return runtimeDashboardActionMsg{Status: "restarted " + shortModelName(instance.ModelID)}
		default:
			return runtimeDashboardActionMsg{Status: "action failed: unsupported action " + action.Kind}
		}
	}
}

func runtimeDashboardPendingStatus(action runtimeDashboardAction) string {
	switch action.Kind {
	case runtimeActionStart:
		return "starting " + shortModelName(action.ModelID) + "..."
	case runtimeActionStop:
		return "stopping " + shortID(action.InstanceID) + "..."
	case runtimeActionRestart:
		return "restarting " + shortModelName(action.ModelID) + "..."
	default:
		return "running action..."
	}
}

func encodeRuntimeDashboardAction(action runtimeDashboardAction) string {
	return strings.Join([]string{action.Kind, action.Runtime, action.ModelID, action.InstanceID}, runtimeActionSep)
}

func parseRuntimeDashboardAction(key string) (runtimeDashboardAction, error) {
	parts := strings.Split(key, runtimeActionSep)
	if len(parts) != 4 {
		return runtimeDashboardAction{}, fmt.Errorf("invalid action key")
	}
	action := runtimeDashboardAction{
		Kind:       parts[0],
		Runtime:    parts[1],
		ModelID:    parts[2],
		InstanceID: parts[3],
	}
	switch action.Kind {
	case runtimeActionStart:
		if action.Runtime == "" || action.ModelID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("start needs runtime and model")
		}
	case runtimeActionStop:
		if action.InstanceID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("stop needs instance")
		}
	case runtimeActionRestart:
		if action.Runtime == "" || action.ModelID == "" || action.InstanceID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("restart needs instance, runtime, and model")
		}
	default:
		return runtimeDashboardAction{}, fmt.Errorf("unsupported action %q", action.Kind)
	}
	return action, nil
}

func canStartRuntimeModel(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) bool {
	return model.Runtime == "llama_server" && model.ID != "" && running.ID == ""
}

func activeInstancesByModel(instances []agentclient.RuntimeInstance) map[string]agentclient.RuntimeInstance {
	active := make(map[string]agentclient.RuntimeInstance, len(instances))
	for _, instance := range instances {
		if instance.Runtime == "" || instance.ModelID == "" || instance.State == "stopped" {
			continue
		}
		active[runtimeModelKey(instance.Runtime, instance.ModelID)] = instance
	}
	return active
}

func runtimeModelKey(runtimeName, modelID string) string {
	return runtimeName + "\x00" + modelID
}

func endpointLabel(endpoint agentclient.RuntimeEndpoint) string {
	name := firstNonEmpty(endpoint.DisplayName, endpoint.ID, endpoint.Kind, "endpoint")
	return "endpoint " + name
}

func endpointValue(endpoint agentclient.RuntimeEndpoint) string {
	parts := nonEmptyParts(endpoint.State, endpoint.Kind, endpoint.Scope, endpoint.BaseURL)
	if endpoint.LatencyMS > 0 {
		parts = append(parts, fmt.Sprintf("%dms", endpoint.LatencyMS))
	}
	if endpoint.LastError != "" {
		parts = append(parts, "error: "+shorten(endpoint.LastError, 48))
	}
	return strings.Join(parts, " | ")
}

func endpointHint(endpoint agentclient.RuntimeEndpoint) string {
	parts := make([]string, 0, 3)
	if endpoint.AuthState != "" {
		parts = append(parts, "auth:"+endpoint.AuthState)
	}
	if len(endpoint.ActiveRoles) > 0 {
		parts = append(parts, strings.Join(endpoint.ActiveRoles, ","))
	}
	if len(endpoint.Models) > 0 {
		parts = append(parts, "models:"+shorten(strings.Join(endpoint.Models, ","), 36))
	}
	return strings.Join(parts, " | ")
}

func modelLabel(model agentclient.RuntimeModel) string {
	return "model " + firstNonEmpty(model.DisplayName, shortModelName(model.ID), model.ID, "unknown")
}

func modelValue(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(model.Runtime, model.DownloadState, model.RuntimeState, model.Family, model.Quantization, formatBytes(model.SizeBytes))
	if running.ID != "" {
		parts = append(parts, "process:"+running.State)
	}
	return strings.Join(parts, " | ")
}

func modelHint(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) string {
	parts := make([]string, 0, 4)
	if model.Active {
		parts = append(parts, "active config")
	}
	caps := runtimeCapabilitiesHint(model.SupportsChat, model.SupportsEmbed, model.SupportsTools)
	if caps != "" {
		parts = append(parts, caps)
	}
	if running.ID != "" {
		parts = append(parts, "pid:"+fmt.Sprint(running.PID))
	}
	return strings.Join(parts, " | ")
}

func instanceLabel(instance agentclient.RuntimeInstance) string {
	return "process " + shortModelName(instance.ModelID)
}

func instanceValue(instance agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(instance.State, instance.Runtime)
	if instance.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid:%d", instance.PID))
	}
	if instance.Endpoint != "" {
		parts = append(parts, instance.Endpoint)
	}
	if instance.RestartCount > 0 {
		parts = append(parts, fmt.Sprintf("restarts:%d", instance.RestartCount))
	}
	if instance.LastError != "" {
		parts = append(parts, "error: "+shorten(instance.LastError, 48))
	}
	return strings.Join(parts, " | ")
}

func instanceHint(instance agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(shortID(instance.ID))
	if !instance.StartedAt.IsZero() {
		parts = append(parts, "started "+relativeTime(instance.StartedAt))
	}
	if instance.LogPath != "" {
		parts = append(parts, filepath.Base(instance.LogPath))
	}
	return strings.Join(parts, " | ")
}

func logLabel(entry agentclient.RuntimeLogEntry) string {
	source := firstNonEmpty(entry.Source, "log")
	if entry.Timestamp.IsZero() {
		return source
	}
	return entry.Timestamp.Format("15:04:05") + " " + source
}

func logHint(entry agentclient.RuntimeLogEntry) string {
	return strings.Join(nonEmptyParts(entry.Level, entry.RuntimeID, shortModelName(entry.ModelID)), " | ")
}

func runtimeCapabilitiesHint(chat, embed, tools bool) string {
	var caps []string
	if chat {
		caps = append(caps, "chat")
	}
	if embed {
		caps = append(caps, "embed")
	}
	if tools {
		caps = append(caps, "tools")
	}
	return strings.Join(caps, ",")
}

func appendMoreRow(rows *[]overlay.Row, label string, count int) {
	*rows = append(*rows, overlay.Row{
		Label:    label,
		Value:    fmt.Sprintf("%d more hidden", count),
		ReadOnly: true,
	})
}

func countLabel(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	plural := singular + "s"
	switch singular {
	case "entry":
		plural = "entries"
	case "process":
		plural = "processes"
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func nonEmptyParts(values ...string) []string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return parts
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shortModelName(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "unknown"
	}
	base := filepath.Base(modelID)
	if base == "." || base == string(filepath.Separator) {
		base = modelID
	}
	return shorten(base, 44)
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func shorten(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func formatBytes(size int64) string {
	if size <= 0 {
		return ""
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}
