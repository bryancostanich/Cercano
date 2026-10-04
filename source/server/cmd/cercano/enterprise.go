package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"cercano/source/server/internal/enterprise"
)

// The connection preview does not enable a managed execution profile until the
// per-inference enforcement layer is integrated. It never changes model config.
func enterpriseCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(out, "Usage: cercano enterprise login --server https://enterprise.example --organization UUID | sync | status | logout\nConnection preview: model enforcement is not enabled by these commands.")
		return nil
	}
	if runtime.GOOS != "darwin" {
		return errors.New("enterprise connection preview currently supports macOS")
	}
	flags := flag.NewFlagSet("enterprise", flag.ContinueOnError)
	server := flags.String("server", "", "trusted enterprise HTTPS origin")
	org := flags.String("organization", "", "organization ID from your administrator")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected enterprise arguments")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	release, err := enterprise.LockConnection(filepath.Join(home, ".cercano", "enterprise", "connection.lock"))
	if err != nil {
		return err
	}
	defer release()
	store, err := enterprise.OpenCredentialStore()
	if err != nil {
		return err
	}
	manager, err := enterprise.New(store, enterprise.Options{ClientVersion: version, CachePath: filepath.Join(home, ".cercano", "enterprise", "active-bundle.json")})
	if err != nil {
		return err
	}
	switch args[0] {
	case "login":
		if err = manager.Login(ctx, *server, *org, func(target string) error { return exec.CommandContext(ctx, "open", target).Run() }); err != nil {
			return err
		}
		err = manager.Sync(ctx)
	case "sync":
		err = manager.Sync(ctx)
	case "status":
	case "logout":
		err = manager.Logout(ctx)
	default:
		return errors.New("unknown enterprise command")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		enterprise.Status
		EnforcementActive bool `json:"enforcement_active"`
	}{Status: manager.Status()})
}
