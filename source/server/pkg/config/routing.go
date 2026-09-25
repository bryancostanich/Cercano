package config

import (
	"fmt"
	"reflect"
	"strings"
)

// Destination identifies execution policy, not provider vendor or hardware.
type Destination string

const (
	DestinationPrimary   Destination = "primary"
	DestinationSecondary Destination = "secondary"
	DestinationLocal     Destination = "local"
)

type Task string

const (
	TaskChat                  Task = "chat"
	TaskCompaction            Task = "compaction"
	TaskDispatch              Task = "dispatch"
	TaskReconnaissance        Task = "reconnaissance"
	TaskMechanicalDevelopment Task = "mechanical_development"
	TaskInvestigation         Task = "investigation"
	TaskImplementation        Task = "implementation"
	TaskReview                Task = "review"
	TaskResearch              Task = "research"
	TaskGitLand               Task = "git_land"
	TaskWatchdog              Task = "watchdog"
)

// TaskAssignment never stores a model ID. Zero fields inherit product defaults.
type TaskAssignment struct {
	Destination Destination `yaml:"destination,omitempty" json:"destination,omitempty"`
	Quality     CostTier    `yaml:"quality,omitempty" json:"quality,omitempty"`
}

// TaskDefinition is shared configuration and UI metadata. Light quality uses
// the existing persisted economy value; it does not create a new model tier.
type TaskDefinition struct {
	Task    Task
	Label   string
	Default TaskAssignment
}

var taskDefinitions = [...]TaskDefinition{
	{TaskChat, "Chat", TaskAssignment{DestinationPrimary, CostStandard}},
	{TaskCompaction, "Compaction", TaskAssignment{DestinationSecondary, CostEconomy}},
	{TaskDispatch, "Default dispatch", TaskAssignment{DestinationSecondary, CostPremium}},
	{TaskReconnaissance, "Reconnaissance", TaskAssignment{DestinationLocal, CostEconomy}},
	{TaskMechanicalDevelopment, "Mechanical development", TaskAssignment{DestinationLocal, CostStandard}},
	{TaskInvestigation, "Investigation", TaskAssignment{DestinationSecondary, CostPremium}},
	{TaskImplementation, "Implementation", TaskAssignment{DestinationSecondary, CostPremium}},
	{TaskReview, "Review", TaskAssignment{DestinationSecondary, CostPremium}},
	{TaskResearch, "Research", TaskAssignment{DestinationSecondary, CostPremium}},
	{TaskGitLand, "Git land", TaskAssignment{DestinationLocal, CostPremium}},
	{TaskWatchdog, "Watchdog", TaskAssignment{DestinationLocal, CostStandard}},
}

// TaskDefinitions returns ordered metadata by value so callers cannot mutate
// product defaults while building editable settings drafts.
func TaskDefinitions() []TaskDefinition {
	return append([]TaskDefinition(nil), taskDefinitions[:]...)
}

func taskDefinition(task Task) (TaskDefinition, bool) {
	for _, def := range taskDefinitions {
		if def.Task == task {
			return def, true
		}
	}
	return TaskDefinition{}, false
}

// ValidTask reports whether a saved or explicit task identity is recognized.
func ValidTask(task Task) bool {
	_, ok := taskDefinition(task)
	return ok
}

func (c Config) TaskAssignment(task Task) TaskAssignment {
	def, ok := taskDefinition(task)
	if !ok {
		// Invalid identities must not acquire Primary or Default dispatch intent.
		// Invocation boundaries must reject them using ValidTask.
		return TaskAssignment{}
	}
	a := def.Default
	if saved, ok := c.TaskAssignments[task]; ok {
		if saved.Destination != "" {
			a.Destination = saved.Destination
		}
		if saved.Quality != "" {
			a.Quality = saved.Quality
		}
	}
	return a
}

// DispatchTokenBudget is the default cumulative billed-token cap (input +
// output summed over every model call) for one delegated dispatch of this
// cost class. Scale: a healthy recon dispatch bills well under 100K tokens;
// the runaway that motivated the budget billed ~3.7M in one dispatch
// (docs/bugs/deepinfra-dispatch-followups.md). UnlimitedDispatchTokenBudget
// disables the cap; main turns — not dispatches — run uncapped.
//
// The sum is dominated by resent history, not by work produced. A measured
// implementation dispatch billed 2,780,649 input against 82,942 output: 97%
// input, ~34x the cumulative cap per unique generated token. Input grows
// roughly quadratically with turn count because every turn resends the
// conversation, so this cap converts to a turn ceiling that is independent of
// how productive those turns are. At ~37K mean input the old 3M Premium
// ceiling bound that dispatch at 74 turns, stopping it mid-implementation with
// its plan still in context.
//
// Premium is therefore sized for sustained implementation rather than for the
// runaway: enough headroom to finish a substantial refactor and verify it,
// while still tripping well before an unbounded loop runs unattended. Economy
// and Standard are unchanged; they bound recon and investigation work, where
// the existing ceilings are already generous.
func (q CostTier) DispatchTokenBudget() int {
	switch q {
	case CostEconomy:
		return 300_000
	case CostStandard:
		return 1_000_000
	case CostPremium:
		return 10_000_000
	}
	return 1_000_000
}

// UnlimitedDispatchTokenBudget explicitly disables the dispatch token cap.
const UnlimitedDispatchTokenBudget = -1

func (q CostTier) CapabilityTier() Tier {
	switch q {
	case CostEconomy:
		return TierFastLight
	case CostStandard:
		return TierEveryday
	case CostPremium:
		return TierMostCapable
	}
	return ""
}

// DispatchDifficultyTier translates an explicit dispatch quality selection.
// Unknown difficulty preserves the historical Economy behavior.
func DispatchDifficultyTier(difficulty string) Tier {
	switch strings.ToLower(strings.TrimSpace(difficulty)) {
	case "":
		return ""
	case "standard":
		return TierEveryday
	case "deep":
		return TierMostCapable
	default:
		return TierFastLight
	}
}
func (c Config) ResolveTask(task Task, difficulty string) TaskAssignment {
	assignment := c.TaskAssignment(task)
	if ValidTask(task) && task != TaskChat {
		if tier := DispatchDifficultyTier(difficulty); tier != "" {
			assignment.Quality, _ = CostTierForCapability(tier)
		}
	}
	return assignment
}

func (c Config) DestinationProfiles(d Destination) (preferred, backup string) {
	switch d {
	case DestinationPrimary:
		backups := c.PrimaryBackups()
		if len(backups) != 0 {
			return c.ActiveCloudProfile, backups[0]
		}
		return c.ActiveCloudProfile, ""
	case DestinationSecondary:
		return c.SecondaryCloudProfile, c.SecondaryBackupCloudProfile
	}
	return "", ""
}

func (c Config) Profile(name string) (CloudProfile, bool) {
	if name != "" {
		for _, p := range c.CloudProfiles {
			if p.Name == name {
				return p, true
			}
		}
	}
	return CloudProfile{}, false
}

// PrimaryBackups returns an independent ordered list, accepting legacy configs.
func (c Config) PrimaryBackups() []string {
	if c.BackupCloudProfiles != nil {
		return append([]string{}, c.BackupCloudProfiles...)
	}
	if c.BackupCloudProfile != "" {
		return []string{c.BackupCloudProfile}
	}
	return nil
}

// SetPrimaryBackups synchronizes the legacy first-entry field, including clears.
// Callers editing user routing must validate the candidate before publishing it.
func (c *Config) SetPrimaryBackups(names []string) {
	c.BackupCloudProfiles = append([]string{}, names...)
	c.BackupCloudProfile = ""
	if len(names) > 0 {
		c.BackupCloudProfile = names[0]
	}
}

// DestinationProfileNames retains configuration order; it does not hide duplicates.
func (c Config) DestinationProfileNames(d Destination) []string {
	if d == DestinationPrimary {
		return append([]string{c.ActiveCloudProfile}, c.PrimaryBackups()...)
	}
	preferred, backup := c.DestinationProfiles(d)
	return []string{preferred, backup}
}

func (c Config) ReferencesProfile(name string) bool {
	if name == "" {
		return false
	}
	for _, d := range []Destination{DestinationPrimary, DestinationSecondary} {
		for _, n := range c.DestinationProfileNames(d) {
			if n == name {
				return true
			}
		}
	}
	return false
}

// ReferencedProfiles deduplicates by identity; credentials stay outside the graph.
func (c Config) ReferencedProfiles() []CloudProfile {
	var profiles []CloudProfile
	seen := map[string]bool{}
	for _, d := range []Destination{DestinationPrimary, DestinationSecondary} {
		for _, name := range c.DestinationProfileNames(d) {
			if p, ok := c.Profile(name); ok && !seen[name] {
				profiles = append(profiles, p)
				seen[name] = true
			}
		}
	}
	return profiles
}

// ValidateDestinationRedirects rejects unsupported edges and cycles globally,
// even when the current task does not traverse the invalid edge.
func (c Config) ValidateDestinationRedirects() error {
	if d := c.SecondaryRedirect; d != "" && d != DestinationPrimary && d != DestinationLocal {
		return fmt.Errorf("invalid secondary redirect %q", d)
	}
	if d := c.LocalRedirect; d != "" && d != DestinationPrimary && d != DestinationSecondary {
		return fmt.Errorf("invalid local redirect %q", d)
	}
	if c.SecondaryRedirect == DestinationLocal && c.LocalRedirect == DestinationSecondary {
		return fmt.Errorf("destination redirect cycle: secondary -> local -> secondary")
	}
	return nil
}

// ResolveDestination follows explicit redirects without changing task quality or
// saved profile bindings. Callers must separately enforce locality and fallback.
// Excluded legacy callers must not call this resolver.
func (c Config) ResolveDestination(d Destination) (Destination, error) {
	if d != DestinationPrimary && d != DestinationSecondary && d != DestinationLocal {
		return "", fmt.Errorf("invalid destination %q", d)
	}
	if err := c.ValidateDestinationRedirects(); err != nil {
		return "", err
	}
	for {
		var next Destination
		switch d {
		case DestinationSecondary:
			next = c.SecondaryRedirect
		case DestinationLocal:
			next = c.LocalRedirect
		}
		if next == "" {
			return d, nil
		}
		d = next
	}
}

func (c Config) ValidateRouting() error {
	if err := c.ValidateDestinationRedirects(); err != nil {
		return err
	}
	for _, d := range []Destination{DestinationPrimary, DestinationSecondary} {
		seen := map[string]bool{}
		for i, name := range c.DestinationProfileNames(d) {
			if name == "" {
				if d == DestinationPrimary && i > 0 {
					return fmt.Errorf("primary backup must not be empty")
				}
				continue
			}
			if seen[name] {
				return fmt.Errorf("%s preferred and backups must differ: duplicate profile %q", d, name)
			}
			seen[name] = true
			if _, ok := c.Profile(name); !ok {
				return fmt.Errorf("%s references missing profile %q", d, name)
			}
		}
	}
	for task, a := range c.TaskAssignments {
		if !ValidTask(task) {
			return fmt.Errorf("unknown task %q", task)
		}
		if a.Destination != "" && a.Destination != DestinationPrimary && a.Destination != DestinationSecondary && a.Destination != DestinationLocal {
			return fmt.Errorf("invalid destination %q", a.Destination)
		}
		if a.Quality != "" && a.Quality.CapabilityTier() == "" {
			return fmt.Errorf("invalid quality %q", a.Quality)
		}
	}
	for _, p := range c.CloudProfiles {
		for quality, model := range p.TierOverrides {
			if quality.CapabilityTier() == "" || model == "" {
				return fmt.Errorf("profile %q has invalid %q override", p.Name, quality)
			}
		}
	}
	return nil
}

// SetTierOverride removes explicit choices on clear; it never copies a default.
func (p *CloudProfile) SetTierOverride(quality CostTier, model string) error {
	if quality.CapabilityTier() == "" {
		return fmt.Errorf("invalid quality %q", quality)
	}
	if model == "" {
		delete(p.TierOverrides, quality)
		return nil
	}
	if p.TierOverrides == nil {
		p.TierOverrides = map[CostTier]string{}
	}
	p.TierOverrides[quality] = model
	return nil
}

func (p CloudProfile) Clone() CloudProfile {
	if p.TierOverrides != nil {
		original := p.TierOverrides
		p.TierOverrides = make(map[CostTier]string, len(original))
		for k, v := range original {
			p.TierOverrides[k] = v
		}
	}
	return p
}

// SetDestinationProfiles validates a complete destination edit before applying it.
func (c *Config) SetDestinationProfiles(d Destination, preferred, backup string) error {
	next := c.Clone()
	switch d {
	case DestinationPrimary:
		next.ActiveCloudProfile = preferred
		if backup == "" {
			next.SetPrimaryBackups(nil)
		} else {
			next.SetPrimaryBackups([]string{backup})
		}
	case DestinationSecondary:
		next.SecondaryCloudProfile, next.SecondaryBackupCloudProfile = preferred, backup
	default:
		return fmt.Errorf("destination %q has no profile bindings", d)
	}
	if err := next.ValidateRouting(); err != nil {
		return err
	}
	*c = next
	return nil
}

func (c *Config) SetTaskAssignment(task Task, assignment *TaskAssignment) error {
	next := c.Clone()
	if !ValidTask(task) {
		return fmt.Errorf("unknown task %q", task)
	}
	if assignment == nil {
		delete(next.TaskAssignments, task)
	} else {
		if next.TaskAssignments == nil {
			next.TaskAssignments = map[Task]TaskAssignment{}
		}
		next.TaskAssignments[task] = *assignment
	}
	if err := next.ValidateRouting(); err != nil {
		return err
	}
	*c = next
	return nil
}

// Equal compares profile configuration by value. Empty sparse overrides are
// equivalent to omission; neither form should invalidate an unchanged login.
func (p CloudProfile) Equal(other CloudProfile) bool {
	if len(p.TierOverrides) == 0 {
		p.TierOverrides = nil
	}
	if len(other.TierOverrides) == 0 {
		other.TierOverrides = nil
	}
	return reflect.DeepEqual(p, other)
}
