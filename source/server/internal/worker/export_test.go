package worker

// export_test.go exposes internal symbols to the worker_test package.
// Only compiled during tests (Go's standard export-test pattern).

import (
	"context"
	"net"

	"google.golang.org/grpc"

	"cercano/source/server/internal/dispatch"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	providerssvc "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/toolstack"
	"cercano/source/server/internal/watchdog"
	pkgcfg "cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

// BuildWorkerWatchdogForTest exposes buildWorkerWatchdog for tests: it builds
// the worker's watchdog from a config snapshot, wired to an engine backed by
// the given resolver's providers.
func BuildWorkerWatchdogForTest(cfg pkgcfg.Config, r providerssvc.Resolver) *watchdog.Watchdog {
	engine := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers {
			return dispatch.Providers{Cloud: r.Cloud(), Open: r.Open()}
		},
		LocusMode: func() locus.Mode { m, _ := locus.ParseMode(cfg.LocusMode); return m },
	})
	return buildWorkerWatchdog(cfg, engine)
}

// NewWorkerRunnerForTest constructs a workerRunner with an injected dial
// function for in-process (bufconn) testing — no real binary needed.
func NewWorkerRunnerForTest(
	persist runner.TurnHistory,
	cfg cfgsvc.Service,
	perms permissions.Broker,
	st secrets.Store,
	dial func(ctx context.Context) (*grpc.ClientConn, error),
) runner.TurnRunner {
	return newWorkerRunnerWithDial(persist, cfg, perms, st, dial)
}

// ResolveCredentialForTest exposes the host's credential resolution logic
// for direct unit testing without needing a running worker stream.
func ResolveCredentialForTest(
	ctx context.Context,
	cfg pkgcfg.Config,
	st secrets.Store,
	profileName string,
) (token, account string, err error) {
	wr := &workerRunner{secrets: st}
	return wr.resolveCredential(ctx, cfg, profileName)
}

// BufconnDial returns a dialFunc that connects via the given bufconn listener.
func BufconnDial(lis interface {
	DialContext(ctx context.Context) (net.Conn, error)
}) func(context.Context) (*grpc.ClientConn, error) {
	return testDialUnix(lis)
}

// WorkerHandleForTest is the exported alias of the pooled worker handle, so a
// test can write a spawn seam with the pool's signature.
type WorkerHandleForTest = workerHandle

// PoolSpawnFunc is the pool's injectable spawn seam, exported for tests.
type PoolSpawnFunc = spawnFunc

// NewWorkerRunnerWithPoolSpawnForTest builds a PRODUCTION-path workerRunner
// (dial == nil, so RunTurn goes through the pool) whose pool spawns via the
// given seam instead of spawnWorker. This lets a test drive the real
// pool-reuse logic (Acquire/Release warm/evict) without spawning OS processes:
// the seam returns bufconn-backed handles and can count spawns.
func NewWorkerRunnerWithPoolSpawnForTest(
	persist runner.TurnHistory,
	cfg cfgsvc.Service,
	perms permissions.Broker,
	st secrets.Store,
	spawn PoolSpawnFunc,
) runner.TurnRunner {
	return &workerRunner{
		persist: persist,
		cfg:     cfg,
		perms:   perms,
		secrets: st,
		pool:    newWorkerPool(spawn),
	}
}

// HandleFromConn wraps a gRPC conn in a *workerHandle with NO backing process
// (cmd == nil). Such a handle is treated as alive by the pool's health check,
// so it is reused warm across turns — exactly what the reuse test needs.
func HandleFromConn(conn *grpc.ClientConn) *WorkerHandleForTest {
	return &workerHandle{conn: conn}
}

// Ensure proto package is used (suppress unused import if needed).
var _ = proto.NewWorkerClient
