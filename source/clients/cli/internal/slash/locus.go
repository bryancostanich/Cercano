package slash

import (
	"context"
	"strings"
	"time"

	"cercano/source/server/pkg/agentclient"
)

var locusModes = map[string]bool{
	"cloud_only": true, "cloud_primary": true, "open_primary": true, "open_only": true,
}

func validLocusMode(s string) bool { return locusModes[s] }

// RegisterLocus wires /locus: view current mode, or set it.
func RegisterLocus(r *Registry, c *agentclient.Client) {
	r.Register(Command{
		Name: "locus",
		Help: "View or set Locus Mode. Usage: /locus [cloud_only|cloud_primary|local_primary|local_only].",
		Handler: func(args []string) Result {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if len(args) == 0 {
				cfg, err := c.GetConfig(ctx)
				if err != nil {
					return Result{Kind: ResultText, Text: "locus: " + err.Error()}
				}
				mode := cfg.LocusMode
				if mode == "" {
					mode = "open_primary"
				}
				return Result{Kind: ResultText, Text: "locus mode: " + mode}
			}
			mode := strings.ToLower(args[0])
			if !validLocusMode(mode) {
				return Result{Kind: ResultText, Text: "invalid mode (want cloud_only|cloud_primary|local_primary|local_only)"}
			}
			msg, err := c.UpdateConfig(ctx, agentclient.ConfigUpdate{LocusMode: mode})
			if err != nil {
				return Result{Kind: ResultText, Text: "locus update failed: " + err.Error()}
			}
			return Result{Kind: ResultText, Text: msg}
		},
	})
}
