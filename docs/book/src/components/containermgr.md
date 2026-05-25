# containermgr

**Source**: `pkg/containermgr/`.

`containermgr` is a thin wrapper around the Docker SDK. Every container-side action sextant takes (spawn, stop, exec, list, manage volumes) goes through here.

## When to reach for this component

- You're touching the agent spawn path and need to add or change a mount, env var, label, or limit.
- You're investigating a "container not found" or "removal in progress" race.
- You want to know how `claude_seed` copy-on-spawn actually works.

## Public surface

| Symbol                                  | File:line                          | Purpose                                                       |
|-----------------------------------------|------------------------------------|---------------------------------------------------------------|
| `Manager`                               | `pkg/containermgr/containermgr.go` | Holds the Docker `client.Client`.                             |
| `New(cfg Config)`                       | `pkg/containermgr/containermgr.go:61` | Connect + ping the daemon.                                 |
| `Run(ctx, spec)`                        | `:221`                             | Create + Start; return `Container{ID, Name}`.                |
| `Stop(ctx, id, grace)`                  | `:305`                             | SIGTERM, wait, SIGKILL, force-remove.                        |
| `Inspect(ctx, id)`                      | `:346`                             | Live `ContainerInfo`.                                         |
| `List(ctx, filter)`                     | `:378`                             | Label-filtered list.                                          |
| `Exec(ctx, id, spec)`                   | `:469`                             | One-shot exec; demuxed stdout/stderr/exit code.               |
| `Logs(ctx, id, tail)`                   | `:529`                             | Tail log lines.                                               |
| `EnsureVolume(ctx, name, labels)`       | `:565`                             | Idempotent named-volume create; returns `created bool`.       |
| `RemoveVolume(ctx, name, force)`        | `:593`                             | Delete a named volume (idempotent on missing).                |
| `VolumeExists(ctx, name)`               | `:611`                             | Existence probe.                                              |
| `PopulateVolumeFromHostDir(ctx, vol, hostSrc, image, cmd)` | `:636`        | One-shot container that copies a host dir into a volume.      |
| `Close()`                               | `:114`                             | Release HTTP transport (idempotent).                          |

## Daemon discovery

`New(cfg Config)` (`pkg/containermgr/containermgr.go:62-70`) resolves the Docker socket in this order:

1. `SEXTANT_DOCKER_SOCKET` env var.
2. `Config.SocketPath`.
3. `detectSocketPath()` (`pkg/containermgr/containermgr.go:99-111`), which probes:
    - `~/.orbstack/run/docker.sock` (OrbStack on macOS).
    - `/var/run/docker.sock` (Docker Desktop / Linux).

A failed `Ping` returns `ErrDaemonUnavailable` (`pkg/containermgr/containermgr.go:32`).

## ContainerSpec

`Run` consumes a `ContainerSpec` (`pkg/containermgr/containermgr.go:154`):

```go
type ContainerSpec struct {
    Name        string             // container name
    Image       string             // e.g. "sextant-sidecar:latest"
    Cmd         []string           // optional command override
    Entrypoint  []string           // optional entrypoint override
    Env         []string           // "K=V" pairs
    Mounts      []MountSpec        // host paths or named volumes
    NetworkMode string             // "bridge" | "host" | etc.
    Resources   ResourceLimits     // CPU shares, memory cap
    Labels      map[string]string  // for List filtering
    AutoRemove  bool               // default true
}
```

`MountSpec` (`pkg/containermgr/containermgr.go:134`) supports both bind mounts (`HostPath`) and named volumes (`VolumeName`). The `ReadOnly` flag applies to either.

## Spawn flow

1. `Run` builds `container.Config` (image, env, labels) and `container.HostConfig` (mounts, network mode, resource limits, `AutoRemove: true`).
2. `cli.ContainerCreate` produces an ID.
3. `cli.ContainerStart` starts it. On failure, the function best-effort-removes the half-created container with `Force=true` (`pkg/containermgr/containermgr.go:285`).
4. `cli.ContainerInspect` resolves the canonical container name.
5. Returns `Container{ID, Name}`.

## Stop flow

`Stop(ctx, id, grace)`:

1. `cli.ContainerStop` (SIGTERM, then SIGKILL after `grace`).
2. `cli.ContainerRemove(Force=true)` to ensure cleanup.
3. "Removal in progress" and "no such container" both treat as success — Docker races us when `AutoRemove=true` is set (`pkg/containermgr/containermgr.go:322`).

## Volumes — for the `.claude` seed flow

The container manager owns the volume lifecycle that backs `template.claude_seed_mode = "copy-on-spawn"`:

- `EnsureVolume(name, labels)` creates a named volume or returns the existing one. The `created` bool tells the caller whether to run the populate step (skip on restart).
- `PopulateVolumeFromHostDir(volumeName, hostSrc, image, cmd)` runs a one-shot container with `User: "0:0"` (forced root), mounts the host source read-only at `/src` and the volume rw at `/dst`, then runs `sh -c "set -e; cp -a /src/. /dst/ && chown -R 1000:1000 /dst"` (`pkg/containermgr/containermgr.go:663`). `cp -a` preserves permissions, ownership, and symlinks. On failure, the error message includes a tail of the one-shot container's logs.
- `RemoveVolume` is idempotent and called on agent archival.

The image used for the populate one-shot is the sidecar image itself by convention — it's already on the host, has `cp` and `chown`, and runs as root by default.

## Exec details

`Exec` (`pkg/containermgr/containermgr.go:469`):

- Creates a Docker exec instance (`ExecCreate`).
- Attaches with `Tty: false`; uses `stdcopy.StdCopy` to demux the multiplexed stream into separate stdout/stderr buffers (same approach `docker exec` uses).
- Returns `ExecResult{Stdout, Stderr, ExitCode}`.

This is the primitive behind both the `exec_in_container` RPC and the `sextant exec <agent> -- <cmd>` CLI verb.

## Test coverage

`pkg/containermgr/containermgr_test.go` runs against a real Docker daemon (it skips on CI when none is available). It covers Run, Stop, List, Exec, EnsureVolume, RemoveVolume, and the populate one-shot.
