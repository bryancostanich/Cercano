package ui

import (
	"strconv"
	"strings"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
)

// knownWatchdogChecks must stay in sync with the check switch in the server's
// buildWatchdogFrom (source/server/internal/server/watchdog_wire.go) and the
// default checks list in pkg/config.
var knownWatchdogChecks = []string{"systematic-debugging", "design-decisions", "verification-strategy", "compute-before-simulate", "commit-checkpoint", "plain-english", "worktree-first", "follow-through"}

// watchdogChecksFromForm reads the current watchdog-check toggle states from
// the live form — the source of truth at commit time, immune to a stale or
// nil cached config. Order follows knownWatchdogChecks. ToggleField renders
// "on"/"off" via Display().
func watchdogChecksFromForm(f *form.Form) []string {
	on := map[string]bool{}
	collect := func(fields []form.Field) {
		for _, fld := range fields {
			if name, ok := strings.CutPrefix(fld.Key(), "watchdog-check-"); ok {
				on[name] = fld.Display() == "on"
			}
		}
	}
	for _, s := range f.Sections {
		collect(s.Fields)
		for _, g := range s.Groups {
			collect(g.Fields)
		}
	}
	out := []string{}
	for _, c := range knownWatchdogChecks {
		if on[c] {
			out = append(out, c)
		}
	}
	return out
}

func hasCheck(list []string, name string) bool {
	for _, c := range list {
		if c == name {
			return true
		}
	}
	return false
}

// toggleCheck returns the new active-checks list with `name` added or removed,
// ordered by knownWatchdogChecks for determinism.
func toggleCheck(current []string, name string, on bool) []string {
	want := map[string]bool{}
	for _, c := range current {
		want[c] = true
	}
	want[name] = on
	out := []string{}
	for _, c := range knownWatchdogChecks {
		if want[c] {
			out = append(out, c)
		}
	}
	return out
}

// encodeChecks joins the list for the sparse update, using "-" for empty
// (distinguishing it from "" = unchanged).
func encodeChecks(list []string) string {
	if len(list) == 0 {
		return "-"
	}
	return strings.Join(list, ",")
}

// accentColorOptions lists the palette tokens accepted by Model.resolvePromptColor.
// Value tokens use the "palette:<key>" shape; the hex escape hatch stays on /color.
func accentColorOptions() []form.Option {
	return []form.Option{
		{Label: "accent (lime)", Value: "palette:accent"},
		{Label: "primary (amber)", Value: "palette:primary"},
		{Label: "info (cyan)", Value: "palette:info"},
		{Label: "bright", Value: "palette:bright"},
		{Label: "muted", Value: "palette:muted"},
		{Label: "border", Value: "palette:border"},
	}
}

func buildSettingsSections(cfg *agentclient.Config, mode, accentToken string) []form.Section {
	return []form.Section{
		{Title: "Routing", Fields: []form.Field{
			form.NewSelect("locus-mode", "locus-mode", []form.Option{
				{Label: "cloud_only", Value: "cloud_only"},
				{Label: "cloud_primary", Value: "cloud_primary"},
				{Label: "open_primary", Value: "open_primary"},
				{Label: "open_only", Value: "open_only"},
			}, cfg.LocusMode),
		}},
		{Title: "Permissions", Fields: []form.Field{
			form.NewSelect("permission-mode", "permission-mode", []form.Option{
				{Label: "strict", Value: "strict"},
				{Label: "permissive", Value: "permissive"},
				{Label: "bypass", Value: "bypass"},
			}, mode),
		}},
		{Title: "Server", Fields: []form.Field{
			form.NewReadOnly("port", "port", cfg.Port, "(read-only)"),
		}},
	}
}

// buildDevFields builds the watchdog controls. They render as the "Watchdog"
// group inside the Development Tools section pinned to the bottom of the
// settings page (see settingsPage.snapshotSections).
func buildDevFields(cfg *agentclient.Config) []form.Field {
	devFields := []form.Field{
		form.NewToggle("watchdog-enabled", "watchdog-enabled", cfg.WatchdogEnabled),
		form.NewToggle("watchdog-echo", "watchdog-echo", cfg.WatchdogEcho),
		form.NewSelect("watchdog-mode", "watchdog-mode", []form.Option{
			{Label: "challenge-and-justify", Value: "challenge-and-justify"},
			{Label: "strict", Value: "strict"},
		}, cfg.WatchdogMode),
	}
	for _, name := range knownWatchdogChecks {
		devFields = append(devFields, form.NewToggle("watchdog-check-"+name, name, hasCheck(cfg.WatchdogChecks, name)))
	}
	devFields = append(devFields, form.NewText("watchdog-escalate-after", "escalate-after", strconv.Itoa(cfg.WatchdogEscalateAfter), ""))
	return devFields
}

type commitKind int

const (
	commitNoop commitKind = iota
	commitConfig
	commitPermission
	commitColor
)

type commitAction struct {
	kind   commitKind
	update agentclient.ConfigUpdate
	value  string
}

// classifyCommit maps a committed (key,value) to the sink that should apply it.
// currentChecks is the current WatchdogChecks list (needed for check-toggle prefix).
func classifyCommit(key, value string, currentChecks []string) commitAction {
	if name, ok := strings.CutPrefix(key, "watchdog-check-"); ok {
		var u agentclient.ConfigUpdate
		u.WatchdogChecks = encodeChecks(toggleCheck(currentChecks, name, value == "true"))
		return commitAction{kind: commitConfig, update: u}
	}
	var u agentclient.ConfigUpdate
	switch key {
	case "local-runtime":
		u.OpenRuntime = value
	case "local-model":
		u.OpenModel = value
	case "ollama-url":
		u.OllamaURL = value
	case "locus-mode":
		u.LocusMode = value
	case "watchdog-enabled":
		u.WatchdogEnabled = value
	case "watchdog-echo":
		u.WatchdogEcho = value
	case "watchdog-mode":
		u.WatchdogMode = value
	case "watchdog-escalate-after":
		u.WatchdogEscalateAfter = value
	case "elide-tool-results":
		u.ElideToolResults = value
	case "lossy-tool-elision":
		u.LossyToolElision = value
	case "raw-retention-days":
		u.RawRetentionDays = value
	case "compacted-retention-days":
		u.CompactedRetentionDays = value
	case "keep-forever":
		u.KeepForever = value
	case "compaction-enabled":
		u.CompactionEnabled = value
	case "tool-loop-max-iterations":
		u.ToolLoopMaxIterations = value
	case "permission-mode":
		return commitAction{kind: commitPermission, value: value}
	case "accent-color":
		return commitAction{kind: commitColor, value: value}
	default:
		return commitAction{kind: commitNoop}
	}
	return commitAction{kind: commitConfig, update: u}
}

// buildRuntimeSections renders the Runtime tab: the active open runtime, its
// default open model, and the Ollama endpoint. These commit through the same
// classifyCommit keys the setup wizard uses (local-runtime, local-model,
// ollama-url), so no new server plumbing is needed.
func buildRuntimeSections(cfg *agentclient.Config) []form.Section {
	return []form.Section{
		{Title: "Open Runtime", Fields: []form.Field{
			form.NewSelect("local-runtime", "runtime", []form.Option{
				{Label: "llama_server", Value: "llama_server"},
				{Label: "ollama", Value: "ollama"},
			}, cfg.OpenRuntime),
			form.NewText("local-model", "default-model", cfg.OpenModel, "the everyday open model"),
		}},
		{Title: "Ollama", Fields: []form.Field{
			form.NewText("ollama-url", "url", cfg.OllamaURL, ""),
		}},
	}
}
