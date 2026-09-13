// Package setupresetcmd implements the terminal-only developer reset surface.
package setupresetcmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

type Outcome struct {
	ResultUnknown      bool
	CredentialsRemoved int
	ConfigWritten      bool
	LiveApplied        bool
	WizardWritten      bool
	UsedAgent          bool
	Warning            string
}

func Run(ctx context.Context, args []string, in io.Reader, out, errout io.Writer, interactive bool, execute func(context.Context) (Outcome, error)) int {
	flags := flag.NewFlagSet("cercano reset", flag.ContinueOnError)
	flags.SetOutput(errout)
	scope := flags.Bool("setup", false, "reset setup/model settings and Cercano credentials; preserve history and downloaded models")
	flags.Usage = func() {
		fmt.Fprintln(errout, "Usage: cercano reset --setup\n\nDeveloper reset. Other sessions may remain open. You control their lifecycle.\nClears setup/model settings, routing, cloud profiles, stored Cercano credentials and wizard answers.\nPreserves conversations, history, preferences, installed runtimes and downloaded models.\nRequires terminal confirmation; does not stop or drain sessions.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if !*scope || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if !interactive {
		fmt.Fprintln(errout, "Setup reset requires an interactive terminal confirmation; nothing changed.")
		return 1
	}
	fmt.Fprintln(out, "This clears setup/model settings, routing, cloud profiles, Cercano credentials and previous setup answers.")
	fmt.Fprintln(out, "Conversations, history, preferences, installed runtimes and downloaded model files are preserved.")
	fmt.Fprintln(out, "Other sessions stay open. In-flight work may fail or write old settings/tokens back; you control session activity.")
	fmt.Fprintln(out, "Custom model paths return to defaults; files at those paths are not deleted.")
	fmt.Fprint(out, "Type RESET to continue: ")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "RESET" {
		fmt.Fprintln(errout, "Setup reset cancelled; nothing changed.")
		return 1
	}
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(errout, "Setup reset cancelled before execution.")
		return 1
	}
	result, err := execute(ctx)
	if err != nil {
		if result.ResultUnknown {
			fmt.Fprintf(errout, "Agent reset result is unknown: %v\nNo local fallback was attempted. Inspect the agent or rerun to finish; do not assume nothing changed.\n", err)
			return 1
		}
		fmt.Fprintf(errout, "Setup reset incomplete: %v\nCredentials removed: %d; live settings applied: %t; config written: %t; fresh wizard written: %t.\nFix the reported error and rerun to finish; completed steps are not rolled back.\n", err, result.CredentialsRemoved, result.LiveApplied, result.ConfigWritten, result.WizardWritten)
		return 1
	}
	fmt.Fprintf(out, "Setup reset complete. Removed %d Cercano credential entries. Existing sessions were not closed.\n", result.CredentialsRemoved)
	if result.Warning != "" {
		fmt.Fprintln(out, "Note:", result.Warning)
	}
	fmt.Fprintln(out, "Start your usual interactive Cercano client (cercano-cli) to begin fresh setup. Custom model directories may need to be re-added.")
	return 0
}
