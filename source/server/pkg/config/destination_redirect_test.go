package config

import (
	"path/filepath"
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
			got, err := c.ResolveDestination(tc.start)
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

func TestDestinationRedirectPreservesAssignmentAndBindings(t *testing.T) {
	c := Config{SecondaryRedirect: DestinationLocal, LocalRedirect: DestinationPrimary,
		ActiveCloudProfile: "primary", BackupCloudProfile: "primary-backup",
		SecondaryCloudProfile: "secondary", SecondaryBackupCloudProfile: "secondary-backup",
		TaskAssignments: map[Task]TaskAssignment{TaskDispatch: {Destination: DestinationSecondary, Quality: CostStandard}},
	}
	before := c.Clone()
	a := c.TaskAssignment(TaskDispatch)
	final, err := c.ResolveDestination(a.Destination)
	if err != nil || final != DestinationPrimary {
		t.Fatalf("final=%q err=%v", final, err)
	}
	if !reflect.DeepEqual(c, before) || a.Quality != CostStandard {
		t.Fatal("resolution changed saved configuration or quality")
	}
	c.SecondaryRedirect, c.LocalRedirect = "", ""
	final, err = c.ResolveDestination(a.Destination)
	preferred, backup := c.DestinationProfiles(final)
	if err != nil || preferred != "secondary" || backup != "secondary-backup" {
		t.Fatalf("reset lost bindings: %q %q %v", preferred, backup, err)
	}
	if _, err := c.ResolveDestination("bogus"); err == nil {
		t.Fatal("invalid source accepted")
	}
	c.SecondaryRedirect, c.LocalRedirect = DestinationLocal, DestinationSecondary
	if _, err := c.ResolveDestination(DestinationPrimary); err == nil {
		t.Fatal("resolver ignored invalid graph")
	}
}

func TestDestinationRedirectSaveLoad(t *testing.T) {
	c := routingFixture()
	c.SecondaryRedirect, c.LocalRedirect = DestinationLocal, DestinationPrimary
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, clear := range []bool{false, true} {
		if clear {
			c.SecondaryRedirect, c.LocalRedirect = "", ""
		}
		if err := Save(c, path); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.SecondaryRedirect != c.SecondaryRedirect || got.LocalRedirect != c.LocalRedirect {
			t.Fatal("save/load lost redirects or reset")
		}
		if got.SecondaryCloudProfile != c.SecondaryCloudProfile || got.SecondaryBackupCloudProfile != c.SecondaryBackupCloudProfile {
			t.Fatal("save/load lost saved bindings")
		}
	}
}
