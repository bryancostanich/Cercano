package config

import (
	"fmt"
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
	TaskChat     Task = "chat"
	TaskDispatch Task = "dispatch"
)

// TaskAssignment never stores a model ID. Zero fields inherit product defaults.
type TaskAssignment struct {
	Destination Destination `yaml:"destination,omitempty" json:"destination,omitempty"`
	Quality     CostTier    `yaml:"quality,omitempty" json:"quality,omitempty"`
}

func (c Config) TaskAssignment(task Task) TaskAssignment {
	a := TaskAssignment{Destination: DestinationPrimary, Quality: CostPremium}
	if task == TaskDispatch {
		a.Destination = DestinationSecondary
	}
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

// ResolveTask applies only explicit dispatch difficulty to the saved assignment.
// Unknown difficulty preserves the historical Economy behavior.
func (c Config) ResolveTask(task Task, difficulty string) TaskAssignment {
	a := c.TaskAssignment(task)
	if task == TaskDispatch && difficulty != "" {
		switch strings.ToLower(difficulty) {
		case "standard":
			a.Quality = CostStandard
		case "deep":
			a.Quality = CostPremium
		default:
			a.Quality = CostEconomy
		}
	}
	return a
}

func (c Config) DestinationProfiles(d Destination) (preferred, backup string) {
	switch d {
	case DestinationPrimary:
		return c.ActiveCloudProfile, c.BackupCloudProfile
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

func (c Config) ReferencesProfile(name string) bool {
	return name != "" && (name == c.ActiveCloudProfile || name == c.BackupCloudProfile || name == c.SecondaryCloudProfile || name == c.SecondaryBackupCloudProfile)
}

// ReferencedProfiles deduplicates by identity and does not substitute missing
// profiles. Credentials remain outside this graph and are fetched by name.
func (c Config) ReferencedProfiles() []CloudProfile {
	var profiles []CloudProfile
	seen := map[string]bool{}
	for _, d := range []Destination{DestinationPrimary, DestinationSecondary} {
		preferred, backup := c.DestinationProfiles(d)
		for _, name := range []string{preferred, backup} {
			if p, ok := c.Profile(name); ok && !seen[name] {
				profiles = append(profiles, p)
				seen[name] = true
			}
		}
	}
	return profiles
}

func (c Config) ValidateRouting() error {
	for _, d := range []Destination{DestinationPrimary, DestinationSecondary} {
		preferred, backup := c.DestinationProfiles(d)
		if preferred != "" && preferred == backup {
			return fmt.Errorf("%s preferred and backup must differ", d)
		}
		for _, name := range []string{preferred, backup} {
			if name != "" {
				if _, ok := c.Profile(name); !ok {
					return fmt.Errorf("%s references missing profile %q", d, name)
				}
			}
		}
	}
	for task, a := range c.TaskAssignments {
		if task != TaskChat && task != TaskDispatch {
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
		next.ActiveCloudProfile, next.BackupCloudProfile = preferred, backup
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
	if task != TaskChat && task != TaskDispatch {
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
