package inference

import (
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
	"testing"
)

func TestDestinationRedirectSelection(t *testing.T) {
	p, s, l := config.DestinationPrimary, config.DestinationSecondary, config.DestinationLocal
	for _, tt := range []struct {
		name                         string
		from, secondary, local, want config.Destination
	}{
		{"secondary-primary", s, p, "", p}, {"secondary-local", s, l, "", l},
		{"local-primary", l, "", p, p}, {"local-secondary", l, "", s, s},
		{"secondary-local-primary", s, l, p, p}, {"local-secondary-primary", l, p, s, p},
		{"disabled-secondary", s, "", "", s}, {"disabled-local", l, "", "", l},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config.Config{SecondaryRedirect: tt.secondary, LocalRedirect: tt.local}
			tiers := Tiers{Cloud: stubLLM{"p"}, Open: stubLLM{"l"}, ResolveDestination: c.ResolveDestination, Destinations: map[config.Destination]Candidate{
				p: {Provider: stubLLM{"p"}, Profile: "primary", IsCloud: true}, s: {Provider: stubLLM{"s"}, Profile: "secondary", IsCloud: true},
			}}
			sel, err := SelectDestination(locus.CloudPrimary, tt.from, tiers)
			if err != nil || sel.Destination != tt.want {
				t.Fatalf("got %+v, %v; want %s", sel, err, tt.want)
			}
			wantName := map[config.Destination]string{p: "p", s: "s", l: "l"}[tt.want]
			if sel.Provider.Name() != wantName || sel.FellBack {
				t.Fatalf("incorrect route: %+v", sel)
			}
			// Placement policy is evaluated against the final route, not the origin.
			forbidden := locus.OpenOnly
			if tt.want == l {
				forbidden = locus.CloudOnly
			}
			// Primary retains its locus policy (open_only selects Local), unlike Secondary.
			if tt.want != p {
				if _, err := SelectDestination(forbidden, tt.from, tiers); err == nil {
					t.Fatal("final placement prohibition bypassed")
				}
			}
		})
	}
}

func TestDestinationRedirectFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		c    config.Config
		from config.Destination
	}{
		{"cycle", config.Config{SecondaryRedirect: config.DestinationLocal, LocalRedirect: config.DestinationSecondary}, config.DestinationLocal},
		{"missing-secondary", config.Config{LocalRedirect: config.DestinationSecondary}, config.DestinationLocal},
		{"missing-local", config.Config{SecondaryRedirect: config.DestinationLocal}, config.DestinationSecondary},
		{"invalid-target", config.Config{LocalRedirect: "invalid"}, config.DestinationLocal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tiers := Tiers{Cloud: stubLLM{"primary"}, ResolveDestination: tt.c.ResolveDestination}
			// A healthy source must never become an implicit fallback for its redirect.
			if tt.from == config.DestinationLocal {
				tiers.Open = stubLLM{"source-local"}
			} else {
				tiers.Destinations = map[config.Destination]Candidate{config.DestinationSecondary: {Provider: stubLLM{"source-secondary"}, IsCloud: true}}
			}
			if sel, err := SelectDestination(locus.CloudPrimary, tt.from, tiers); err == nil {
				t.Fatalf("unexpected selection %+v", sel)
			}
		})
	}
}
