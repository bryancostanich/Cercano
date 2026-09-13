package setupresetcmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/setupreset"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var ErrNoAgent = errors.New("no agent is listening")

type Dependencies struct {
	ConfigPath string
	WizardPath string
	OpenStore  func() (secrets.Store, error)
	// TryAgent must return ErrNoAgent only when no listener is present. Never
	// fall back locally after an RPC may already have performed the reset.
	TryAgent func(context.Context, string) (Outcome, error)
}

func Perform(ctx context.Context, d Dependencies) (Outcome, error) {
	var outcome Outcome
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	// Environment-overridden wizard paths must never replace the config itself.
	if filepath.Clean(d.ConfigPath) == filepath.Clean(d.WizardPath) {
		return outcome, fmt.Errorf("config and wizard paths must be different files")
	}
	configInfo, _ := os.Stat(d.ConfigPath)
	wizardInfo, _ := os.Stat(d.WizardPath)
	if configInfo != nil && wizardInfo != nil && os.SameFile(configInfo, wizardInfo) {
		return outcome, fmt.Errorf("config and wizard paths refer to the same file")
	}
	prepared, err := setupreset.Prepare(d.ConfigPath)
	if err != nil {
		return outcome, err
	}
	addr := net.JoinHostPort("127.0.0.1", prepared.Config.Port)
	err = ErrNoAgent
	if d.TryAgent != nil {
		outcome, err = d.TryAgent(ctx, addr)
	}
	if errors.Is(err, ErrNoAgent) {
		if d.OpenStore == nil {
			return outcome, fmt.Errorf("Cercano credential store is unavailable")
		}
		store, openErr := d.OpenStore()
		if openErr != nil {
			return outcome, fmt.Errorf("open Cercano credential store: %w", openErr)
		}
		result, resetErr := setupreset.Reset(ctx, d.ConfigPath, store, nil)
		outcome = Outcome{CredentialsRemoved: result.DeletedCredentials, ConfigWritten: result.ConfigWritten}
		err = resetErr
	}
	if err != nil {
		return outcome, err
	}
	if err = setupreset.WriteFreshWizard(d.WizardPath); err != nil {
		return outcome, fmt.Errorf("settings reset, but fresh wizard state could not be written: %w", err)
	}
	outcome.WizardWritten = true
	return outcome, nil
}

// TryExistingAgent contacts the configured loopback agent without starting one.
// Connection refusal selects local reset; errors from a reachable agent never
// trigger a second local operation with potentially different cached state.
func TryExistingAgent(ctx context.Context, addr string) (Outcome, error) {
	var outcome Outcome
	dialer := net.Dialer{Timeout: time.Second}
	probe, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return outcome, ErrNoAgent
		}
		return outcome, fmt.Errorf("contact existing agent: %w", err)
	}
	probe.Close()
	outcome.UsedAgent = true
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return outcome, err
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := proto.NewAgentClient(conn).ResetSetup(callCtx, &proto.ResetSetupRequest{Confirmed: true})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return outcome, fmt.Errorf("the running agent does not support setup reset; update/restart that agent and let sessions reconnect before retrying")
		}
		outcome.ResultUnknown = true
		return outcome, fmt.Errorf("agent reset failed or its result is unknown; no local fallback was attempted: %w", err)
	}
	outcome.CredentialsRemoved = int(result.GetCredentialsRemoved())
	outcome.ConfigWritten = result.GetConfigWritten()
	outcome.LiveApplied = result.GetLiveApplied()
	outcome.Warning = result.GetWarning()
	if !result.GetOk() {
		return outcome, fmt.Errorf("%s", result.GetError())
	}
	return outcome, nil
}
