// Package chatroute defines a conversation-scoped, explicit main-chat target.
// It never changes task-routing defaults or delegated/background work.
package chatroute

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

type Route struct {
	Profile string `json:"profile"`
	Model   string `json:"model"`
}

type Request struct {
	Action  string `json:"action"`
	Profile string `json:"profile,omitempty"`
	Model   string `json:"model,omitempty"`
}

type Profile struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"configured_model"`
}

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Status struct {
	Models   []Model   `json:"models,omitempty"`
	Override *Route    `json:"override"`
	Profiles []Profile `json:"profiles,omitempty"`
	Note     string    `json:"note"`
}

type Control func(context.Context, string, Request) (Status, error)

type Store interface {
	ChatRoute(context.Context, string) (*Route, error)
	SetChatRoute(context.Context, string, *Route) error
}

type Resolver interface {
	ResolveChatRoute(context.Context, Route) (inference.Provider, error)
}

// ProfileFor validates the explicit target without changing the saved profile.
// An explicit cloud choice must not bypass the local-only privacy boundary.
func ProfileFor(cfg config.Config, route Route) (config.CloudProfile, error) {
	mode, err := locus.ParseMode(cfg.LocusMode)
	if err != nil {
		return config.CloudProfile{}, err
	}
	if mode == locus.OpenOnly {
		return config.CloudProfile{}, fmt.Errorf("session chat override requires cloud access; current locus is %s", cfg.LocusMode)
	}
	if strings.TrimSpace(route.Profile) == "" || strings.TrimSpace(route.Model) == "" {
		return config.CloudProfile{}, fmt.Errorf("session chat override requires a saved profile name and exact model ID")
	}
	p, ok := cfg.Profile(route.Profile)
	if !ok {
		return config.CloudProfile{}, fmt.Errorf("cloud profile %q not found", route.Profile)
	}
	p.Model = route.Model
	return p, nil
}

// Build constructs one provider, not a profile backup chain, and pins its model.
func Build(cfg config.Config, route Route, build func(config.CloudProfile) (inference.Provider, error)) (inference.Provider, error) {
	p, err := ProfileFor(cfg, route)
	if err != nil {
		return nil, err
	}
	provider, err := build(p)
	if err != nil {
		return nil, fmt.Errorf("session chat override %s / %s: %w", route.Profile, route.Model, err)
	}
	provider = profilechain.BindAccount(provider, route.Profile, string(config.DestinationPrimary))
	assignment := cfg.TaskAssignment(config.TaskChat)
	return inference.WithTaskRoute(provider, config.TaskChat, assignment, config.DestinationPrimary, route.Model), nil
}
