package agent

import (
	"fmt"
	"sort"
	"sync"
)

// ProfileBroker holds the one active capability Profile for a session and
// resolves profile names to Profile values. It is the profile analogue of the
// permission Broker: where the permission broker owns the confirm-aggressiveness
// mode (strict/permissive/bypass), this owns the orthogonal "which tools are
// available at all" posture.
//
// The design deliberately makes this a *named, single active profile* rather
// than one boolean per mode. A session is in exactly one profile at a time
// (default = unrestricted), so contradictory states like "planning AND
// executing" are unrepresentable by construction. Adding a future mode
// (brainstorm, execute, a sandboxed review posture, …) is one Register call —
// no new broker, no new RPC, no change to the runner, which only ever asks for
// "the active profile" and never names one.
//
// A profile carries only the capability fence (see profile.go). It does NOT
// carry behavioral shaping — a brainstorming prompt, or execution's control
// loop — those are separate concerns layered on top by their own subsystems.
// The broker's job is solely: which Profile is active, and what does a name
// resolve to.
//
// ProfileBroker is safe for concurrent use.
type ProfileBroker struct {
	mu       sync.RWMutex
	active   map[string]string  // conversationID → profile name; "" means default
	registry map[string]Profile // name → profile; excludes the default
}

// DefaultProfileName is the reserved name for "no fence" — the unrestricted
// posture a session runs in normally. SetActive("") and SetActive("default")
// both select it. It is never stored in the registry.
const DefaultProfileName = "default"

// NewProfileBroker returns a broker seeded with the built-in profiles (currently
// just "plan"). Every conversation starts in the default (unrestricted) profile;
// the active pointer is keyed by conversation ID so one client entering planning
// mode does not fence any other attached client.
func NewProfileBroker() *ProfileBroker {
	b := &ProfileBroker{
		active:   make(map[string]string),
		registry: make(map[string]Profile),
	}
	b.Register(PlanProfile())
	return b
}

// Register adds or replaces a named profile. The name comes from p.Name; a
// profile named "" or "default" is rejected — that name is reserved for the
// unrestricted posture and must not be shadowed by a fence.
func (b *ProfileBroker) Register(p Profile) {
	if p.Name == "" || p.Name == DefaultProfileName {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.registry[p.Name] = p
}

// SetActive selects the active profile for one conversation by name. "" and
// "default" clear the fence for that conversation (delete its entry, so it
// resolves back to the unrestricted posture); any other name must be
// registered, or an error is returned and the active profile is left unchanged.
// The active pointer is per-conversation: switching one conversation's profile
// never affects another attached client.
func (b *ProfileBroker) SetActive(convID, name string) error {
	if name == "" || name == DefaultProfileName {
		b.mu.Lock()
		delete(b.active, convID)
		b.mu.Unlock()
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.registry[name]; !ok {
		return fmt.Errorf("unknown profile %q (known: %s)", name, b.namesLocked())
	}
	b.active[convID] = name
	return nil
}

// Active returns the active Profile for a conversation. When the conversation
// has no fence set this is the zero Profile, which restricts nothing (see
// Profile.Restricts).
func (b *ProfileBroker) Active(convID string) Profile {
	b.mu.RLock()
	defer b.mu.RUnlock()
	name := b.active[convID]
	if name == "" {
		return Profile{}
	}
	return b.registry[name]
}

// ActiveName returns the conversation's active profile name, or
// DefaultProfileName when it is in the unrestricted posture.
func (b *ProfileBroker) ActiveName(convID string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	name := b.active[convID]
	if name == "" {
		return DefaultProfileName
	}
	return name
}

// Names returns the registered profile names (excluding the default) in sorted
// order — for diagnostics and the /mode command's help.
func (b *ProfileBroker) Names() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.registry))
	for n := range b.registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// namesLocked formats registered names for an error message. Caller holds mu.
func (b *ProfileBroker) namesLocked() string {
	names := make([]string, 0, len(b.registry)+1)
	names = append(names, DefaultProfileName)
	for n := range b.registry {
		names = append(names, n)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
