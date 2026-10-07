package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"cercano/source/server/internal/brewrestart"
	"cercano/source/server/pkg/config"
)

func runUpgradeRestart(args []string) int {
	return upgradeRestartCommand(args, os.Stdout,
		func() (string, error) { cfg, err := config.Load(config.DefaultPath()); return cfg.Port, err },
		func() (string, error) {
			path, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(path)
		},
		brewrestart.RestartInstalled)
}

func upgradeRestartCommand(args []string, out io.Writer, configuredPort func() (string, error), executable func() (string, error), restart func(context.Context, string, netip.AddrPort) (bool, error)) int {
	flags := flag.NewFlagSet("restart-after-upgrade", flag.ContinueOnError)
	flags.SetOutput(out)
	address := flags.String("address", "", "explicit loopback agent address (default: configured port)")
	timeout := flags.Duration("timeout", 2*time.Minute, "deadline for drain and restart (maximum 10m)")
	flags.Usage = func() {
		fmt.Fprintln(out, "Usage: cercano restart-after-upgrade [--address 127.0.0.1:PORT] [--timeout 2m]\nRestart an existing agent owned by this Homebrew installation after a successful upgrade.\nDoes not start an absent agent, install updates, or compare versions.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *timeout <= 0 || *timeout > 10*time.Minute {
		fmt.Fprintln(out, "Invalid arguments or restart deadline.")
		return 2
	}
	if *address == "" {
		port, err := configuredPort()
		if err != nil {
			fmt.Fprintln(out, "Cannot load agent configuration; no restart attempted.")
			return 1
		}
		*address = "127.0.0.1:" + port
	}
	endpoint, err := netip.ParseAddrPort(*address)
	if err != nil || !endpoint.Addr().IsLoopback() || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" {
		fmt.Fprintln(out, "Restart requires a numeric loopback address and nonzero port.")
		return 2
	}
	path, err := executable()
	if err != nil {
		fmt.Fprintln(out, "Cannot resolve installed executable; no restart attempted.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	restarted, err := restart(ctx, path, endpoint)
	if err != nil {
		switch {
		case brewrestart.IsSafeStopBusy(err):
			fmt.Fprintf(out, "Upgrade restart not completed: %v\nThe running agent was left in place with active update-relevant work in progress; nothing was stopped and no replacement was started. The update is installed and takes effect after the agent restarts. Re-run this command once the current work finishes, or use --timeout to wait longer.\n", err)
		case brewrestart.IsSafeStopUnsupported(err):
			fmt.Fprintf(out, "Upgrade restart not completed: %v\nThe running agent predates the safe-stop request and cannot be stopped safely. It was left in place; nothing was stopped and no replacement was started. The update is installed and takes effect after a manual restart (use /restart-agent or stop and start cercano), then future upgrades can restart it safely.\n", err)
		default:
			fmt.Fprintf(out, "Upgrade restart failed: %v\nThe update may already be installed. Check agent logs; use /restart-agent if the old agent is still serving, or start cercano manually if no agent is running.\n", err)
		}
		return 1
	}
	if restarted {
		fmt.Fprintln(out, "Running agent restarted using the installed binary.")
	} else {
		fmt.Fprintln(out, "No running agent owned by this installation; nothing started.")
	}
	return 0
}
