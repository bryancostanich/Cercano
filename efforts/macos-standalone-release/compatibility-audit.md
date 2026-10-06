# Connection compatibility audit

Verified directly against `source/server/pkg/agentclient/client.go`, lines 1–240. This corrects an earlier delegated summary whose line references were inaccurate and which conflated operating-system portability with client/server compatibility.

- `Dial` (lines 88–117) first attempts `connect` with a 600ms timeout. On success it starts the connection watcher. On failure it calls `ensureServerLaunched`, then connects with a 3s timeout.
- `connect` (lines 125–142) uses blocking gRPC transport connection with insecure transport credentials and 64 MiB message limits. It returns a generated AgentClient without calling an application-level handshake or comparing versions in this function.
- `ensureServerLaunched` (lines 144–167) serializes launches with a lock, retries connection, and launches if that connection fails. This slice does not distinguish application incompatibility from transport unavailability.
- `DialExisting` (lines 121–123) only calls `connect`; it does not launch a process.

## Scope limits

This is not proof that the full application lacks compatibility checks. Later CLI initialization, protocol definitions, and reconnect behavior still require inspection. No protocol changes are authorized by this finding alone. There is no runtime probe yet demonstrating the behavior with an incompatible server.

The follow-up handshake search found no `.proto` matches in the generated package directory; this does not establish that protocol sources do not exist elsewhere. Locate the real protocol source before drawing a conclusion.
