package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func redirectConfig(t *testing.T, secondary, local string) Config {
	t.Helper()
	var c Config
	if err := yaml.Unmarshal([]byte("secondary_redirect: \""+secondary+"\"\nlocal_redirect: \""+local+"\"\n"), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDestinationRedirectResolution(t *testing.T) {
	for _, tc := range []struct {
		name, secondary, local string
		start, want            Destination
	}{
		{"primary unchanged", "", "", DestinationPrimary, DestinationPrimary},
		{"secondary unchanged", "", "", DestinationSecondary, DestinationSecondary},
		{"local unchanged", "", "", DestinationLocal, DestinationLocal},
		{"secondary primary", "primary", "", DestinationSecondary, DestinationPrimary},
		{"secondary local", "local", "", DestinationSecondary, DestinationLocal},
		{"local primary", "", "primary", DestinationLocal, DestinationPrimary},
		{"local secondary", "", "secondary", DestinationLocal, DestinationSecondary},
		{"secondary local primary", "local", "primary", DestinationSecondary, DestinationPrimary},
		{"local secondary primary", "primary", "secondary", DestinationLocal, DestinationPrimary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := redirectConfig(t, tc.secondary, tc.local)
			if err := c.ValidateRouting(); err != nil {
				t.Fatal(err)
			}
			// A runtime assertion keeps the pre-implementation reproduction compilable.
			resolver, ok := any(c).(interface {
				ResolveDestination(Destination) (Destination, error)
			})
			if !ok {
				t.Fatal("Config has no shared destination redirect resolver")
			}
			got, err := resolver.ResolveDestination(tc.start)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestDestinationRedirectValidation(t *testing.T) {
	for _, tc := range []struct{ name, secondary, local string }{
		{"cycle", "local", "secondary"},
		{"secondary self", "secondary", ""},
		{"local self", "", "local"},
		{"unknown secondary target", "bogus", ""},
		{"unknown local target", "", "bogus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := redirectConfig(t, tc.secondary, tc.local)
			before := c.Clone()
			if err := c.ValidateRouting(); err == nil {
				t.Fatal("invalid redirect accepted")
			}
			if !reflect.DeepEqual(c, before) {
				t.Fatal("validation mutated configuration")
			}
		})
	}
}

func TestDestinationRedirectPersistence(t *testing.T) {
	c := redirectConfig(t, "local", "primary")
	encoded, err := yaml.Marshal(c.Clone())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"secondary_redirect: local", "local_redirect: primary"} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("cloned YAML lost %q", want)
		}
	}
	empty, err := yaml.Marshal(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "_redirect:") {
		t.Fatal("unset redirects must remain sparse")
	}
}
