//go:build darwin && cgo

package brewrestart

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// RestartInstalled restarts only a running, owned installation at endpoint.
// It does not install an update, compare versions, register a service, or
// start an absent agent. The caller must set a bounded context deadline.
func RestartInstalled(ctx context.Context, newExecutable string, endpoint netip.AddrPort) (bool, error) {
	if _, ok := ctx.Deadline(); !ok {
		return false, fmt.Errorf("restart requires a deadline")
	}
	return coordinateRestart(ctx, nativeRestartOps(), newExecutable, uint32(os.Getuid()), endpoint)
}

func nativeRestartOps() restartOps {
	return restartOps{
		source: kernelProcesses{}, capture: CaptureLaunchState, preflight: preflightRestart,
		lock: func(ctx context.Context, s LaunchState) (func(), error) {
			return agentclient.AcquireAutoLaunchLock(ctx, launchTempDir(s))
		},
		shutdown: requestShutdown, waitExit: waitProcessExit, start: startReplacement, ready: waitReplacementReady,
	}
}

func launchTempDir(s LaunchState) string {
	dir := "/tmp"
	for _, entry := range s.Env {
		if value, ok := strings.CutPrefix(entry, "TMPDIR="); ok {
			if value != "" {
				dir = value
			} else {
				dir = "/tmp"
			}
		}
	}
	return dir
}

func preflightRestart(executable string, s LaunchState) error {
	if _, err := FormulaRoot(executable); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil || canonical != executable {
		return fmt.Errorf("replacement executable must be an existing canonical file")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("replacement is not a regular executable")
	}
	// Only actual long-lived agent entrypoints may be replayed. Never replay a
	// worker, setup command, or the coordinator itself from captured argv.
	if len(s.Args) != 1 && !(len(s.Args) == 2 && s.Args[1] == "agent") {
		return fmt.Errorf("unsupported agent launch arguments; refusing automatic restart")
	}
	cwd, err := os.Stat(s.Directory)
	if err != nil || !cwd.IsDir() {
		return fmt.Errorf("original working directory is unavailable")
	}
	if !filepath.IsAbs(launchTempDir(s)) {
		return fmt.Errorf("original temporary directory is not absolute")
	}
	log, err := openRestartLog(s)
	if err != nil {
		return err
	}
	return log.Close()
}

func openRestartLog(s LaunchState) (*os.File, error) {
	path := filepath.Join(launchTempDir(s), "cercano-server.log")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_APPEND|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open restart log: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("restart log is not a regular file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		_ = f.Close()
		return nil, fmt.Errorf("restart log belongs to another user")
	}
	return f, nil
}

// requestShutdown stops the verified agent before its replacement starts.
// Ownership (kernel identity + exact listener endpoint) is checked again
// after connecting, so the replacement's start cannot race a stolen socket.
// The update-related stop is the bounded ShutdownAgentWhenIdle safe-stop RPC
// carrying the PID from the verified identity — never the legacy
// fire-and-forget ShutdownAgent. Work in flight is waited on until the
// restart deadline (never cancelled); an agent predating the safe-stop RPC
// is left running rather than bounced. See safestop.go for typed outcomes.
func requestShutdown(ctx context.Context, id Identity, endpoint netip.AddrPort) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, endpoint.String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return err
	}
	defer conn.Close()
	owns, err := HoldsListener(id, endpoint)
	if err != nil {
		return err
	}
	if !owns {
		return fmt.Errorf("agent no longer owns the target listener")
	}
	// ctx (not dialCtx) bounds the wait for idle: the safe stop drains
	// in-flight work until the overall restart deadline expires.
	return safeStopRequest(ctx, proto.NewAgentClient(conn), id.PID, "Homebrew installation updated")
}

func pauseRestart(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitProcessExit(ctx context.Context, id Identity) error {
	for {
		now, err := Inspect(id.PID)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		if now.StartSeconds != id.StartSeconds || now.StartMicroseconds != id.StartMicroseconds {
			return nil
		} // PID reused: original is gone.
		if !now.SameProcess(id) {
			return fmt.Errorf("original process changed identity without exiting")
		}
		if err := pauseRestart(ctx); err != nil {
			return err
		}
	}
}

func startReplacement(executable string, s LaunchState) (Identity, error) {
	log, err := openRestartLog(s)
	if err != nil {
		return Identity{}, err
	}
	defer log.Close()
	cmd := exec.Command(executable, s.Args[1:]...)
	cmd.Env = s.Env
	cmd.Dir = s.Directory
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Identity{}, err
	}
	go func() { _ = cmd.Wait() }()
	// exec.Cmd.Start has completed the exec handshake; a startup crash must
	// fail instead of treating any other process on the port as a replacement.
	return Inspect(cmd.Process.Pid)
}

func waitReplacementReady(ctx context.Context, id Identity, endpoint netip.AddrPort) error {
	return pollReplacementReady(ctx,
		func() (bool, error) { return HoldsListener(id, endpoint) },
		func(ctx context.Context) error {
			conn, err := grpc.DialContext(ctx, endpoint.String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
			if err == nil {
				_ = conn.Close()
			}
			return err
		})
}

// Socket descriptors may close between proc_pidinfo and proc_pidfdinfo.
// Retry the entire ownership snapshot rather than trusting partial results.
// Every probe still binds the listener to the original process identity.
func pollReplacementReady(ctx context.Context, holds func() (bool, error), dial func(context.Context) error) error {
	transient := func(err error) bool { return errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.EAGAIN) }
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		owns, err := holds()
		if err != nil && !transient(err) {
			return err
		}
		if err == nil && owns {
			dialCtx, cancel := context.WithTimeout(ctx, time.Second)
			err := dial(dialCtx)
			cancel()
			if err == nil {
				owns, err = holds()
				if err != nil && !transient(err) {
					return err
				}
				if err == nil && owns {
					return nil
				}
			}
		}
		if err := pauseRestart(ctx); err != nil {
			return err
		}
	}
}
