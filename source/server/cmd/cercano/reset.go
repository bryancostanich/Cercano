package main

import (
	"context"
	"os"

	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/setupresetcmd"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/setupstate"
	"golang.org/x/term"
)

func runReset(args []string) int {
	return setupresetcmd.Run(context.Background(), args, os.Stdin, os.Stdout, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())), func(ctx context.Context) (setupresetcmd.Outcome, error) {
		return setupresetcmd.Perform(ctx, setupresetcmd.Dependencies{
			ConfigPath: config.DefaultPath(), WizardPath: setupstate.StatePath(),
			OpenStore: secrets.OpenKeychain, TryAgent: setupresetcmd.TryExistingAgent,
		})
	})
}
