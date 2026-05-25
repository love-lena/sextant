package containermgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// SocketEnvVar is the env-var override callers can set to force a
// specific Docker socket path. Empty = auto-detect.
const SocketEnvVar = "SEXTANT_DOCKER_SOCKET"

// ErrDaemonUnavailable is wrapped by every path that fails to talk to
// the Docker daemon. Callers test with errors.Is so the spawn handler
// can surface a clean "no container runtime configured" error to the
// operator.
var ErrDaemonUnavailable = errors.New("containermgr: docker daemon unavailable")

// Manager wraps a Docker API client. One Manager per daemon. Safe for
// concurrent use; the underlying *client.Client multiplexes HTTP/2
// requests over the same Unix socket.
type Manager struct {
	cli *client.Client

	mu     sync.Mutex
	closed bool
}

// Config knobs for New. Empty values use defaults.
type Config struct {
	// SocketPath, when set, overrides auto-detection. Useful for tests
	// and for installs that put the docker socket somewhere unusual.
	SocketPath string
}

// New connects to the Docker daemon. The connection is verified with
// a Ping before New returns, so any error here surfaces immediately
// rather than at the first Run call. Auto-detection order:
//
//  1. SEXTANT_DOCKER_SOCKET env var (operator override).
//  2. Config.SocketPath (caller override).
//  3. ~/.orbstack/run/docker.sock (OrbStack on macOS).
//  4. /var/run/docker.sock (Docker Desktop, dockerd on Linux, Podman
//     compat socket).
//  5. The Docker SDK default (DOCKER_HOST env var or unix socket).
func New(cfg Config) (*Manager, error) {
	path := strings.TrimSpace(cfg.SocketPath)
	if env := strings.TrimSpace(os.Getenv(SocketEnvVar)); env != "" {
		path = env
	}
	if path == "" {
		if p := detectSocketPath(); p != "" {
			path = p
		}
	}

	opts := []client.Opt{
		client.WithAPIVersionNegotiation(),
	}
	switch {
	case path != "":
		opts = append(opts, client.WithHost("unix://"+path))
	default:
		opts = append(opts, client.FromEnv)
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: build client: %w", ErrDaemonUnavailable, err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingCtx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("%w: ping %s: %w", ErrDaemonUnavailable, path, err)
	}
	return &Manager{cli: cli}, nil
}

// detectSocketPath returns the first present candidate socket path or
// "" if no candidate is on disk. The OrbStack socket is tried first so
// macOS dev boxes pick it up automatically.
func detectSocketPath() string {
	candidates := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".orbstack", "run", "docker.sock"))
	}
	candidates = append(candidates, "/var/run/docker.sock")
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.Mode()&os.ModeSocket != 0 {
			return c
		}
	}
	return ""
}

// Close releases the underlying HTTP transport. Idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.cli.Close()
}

// MountSpec is a single mount attached to a container. Two flavors:
//
//   - Bind mount: HostPath set → that host path is bind-mounted at
//     ContainerPath. ReadOnly toggles ro/rw.
//   - Named volume: VolumeName set (and HostPath empty) → the named
//     Docker volume is mounted at ContainerPath. ReadOnly toggles ro/rw.
//
// VolumeName is mutually exclusive with HostPath; setting both is a
// caller error and Run will treat HostPath as the source of truth
// (named volumes were introduced after bind mounts).
type MountSpec struct {
	HostPath      string
	ContainerPath string
	// VolumeName, when non-empty and HostPath is empty, mounts a Docker
	// named volume at ContainerPath. See claude_seed copy-on-spawn flow
	// in pkg/rpc/handlers/spawn.go for the motivating use case.
	VolumeName string
	// ReadOnly = true mounts ro. Defaults to false (rw).
	ReadOnly bool
}

// ResourceLimits caps a container's CPU/memory. Zero values mean "no
// host-enforced cap" (the default).
type ResourceLimits struct {
	CPUShares int64
	MemoryMiB int64
}

// ContainerSpec is the inputs to Run. Sextantd's spawn handler builds
// one from an agent definition + template; tests use it directly.
type ContainerSpec struct {
	// Name is the container name (Docker disambiguates if absent). The
	// spawn handler uses "sextant-<agent-name>-<incarnation-uuid-short>".
	Name string

	// Image is the image reference to run (e.g. "sextant-sidecar:latest").
	Image string

	// Cmd, when non-empty, overrides the image's CMD.
	Cmd []string

	// Entrypoint, when non-empty, overrides the image's ENTRYPOINT.
	Entrypoint []string

	// Env is the env-var set the container starts with.
	Env map[string]string

	// Mounts is the list of bind mounts to attach.
	Mounts []MountSpec

	// NetworkMode picks the Docker network. Empty defaults to the
	// daemon's default bridge (which on OrbStack still gives the
	// container `host.docker.internal` resolution).
	NetworkMode string

	// Resources caps the container's CPU/memory.
	Resources ResourceLimits

	// Labels are stamped onto the container. The spawn handler always
	// sets sextant.agent_uuid, sextant.agent_name, sextant.host_id, and
	// sextant.incarnation_id. Tests add sextant.test_run for cleanup.
	Labels map[string]string

	// AutoRemove asks Docker to remove the container automatically when
	// it exits. We default to true for sidecar containers so a crash
	// doesn't leave a stopped container haunting `docker ps -a`.
	AutoRemove bool
}

// Container is the live handle returned by Run. ID is the Docker
// container id (long form). Name is the resolved container name.
type Container struct {
	ID   string
	Name string
}

// ContainerInfo is the projection Inspect / List return. Status is the
// Docker status string ("running", "exited", ...).
type ContainerInfo struct {
	ID       string
	Name     string
	Image    string
	Status   string
	Labels   map[string]string
	Created  time.Time
	ExitCode int
}

// Filter narrows a List call. Labels is the most useful field — every
// entry is ANDed with the others via Docker's filter argument set. An
// empty Filter matches every container.
type Filter struct {
	Labels map[string]string
}

// Run starts a fresh container from spec. Returns the live handle on
// success or a wrapped ErrDaemonUnavailable if the daemon refused.
func (m *Manager) Run(ctx context.Context, spec ContainerSpec) (*Container, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if strings.TrimSpace(spec.Image) == "" {
		return nil, fmt.Errorf("containermgr: spec.Image is required")
	}

	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	cfg := &container.Config{
		Image:  spec.Image,
		Env:    env,
		Labels: spec.Labels,
		Cmd:    spec.Cmd,
	}
	if len(spec.Entrypoint) > 0 {
		cfg.Entrypoint = spec.Entrypoint
	}

	hostMounts := make([]mount.Mount, 0, len(spec.Mounts))
	for _, ms := range spec.Mounts {
		if ms.HostPath == "" && ms.VolumeName != "" {
			// Named-volume mount. Docker resolves the volume by name; the
			// volume must exist (callers create it via EnsureVolume).
			hostMounts = append(hostMounts, mount.Mount{
				Type:     mount.TypeVolume,
				Source:   ms.VolumeName,
				Target:   ms.ContainerPath,
				ReadOnly: ms.ReadOnly,
			})
			continue
		}
		hostMounts = append(hostMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   ms.HostPath,
			Target:   ms.ContainerPath,
			ReadOnly: ms.ReadOnly,
		})
	}
	hostCfg := &container.HostConfig{
		AutoRemove:  spec.AutoRemove,
		Mounts:      hostMounts,
		NetworkMode: container.NetworkMode(spec.NetworkMode),
	}
	if spec.Resources.MemoryMiB > 0 {
		hostCfg.Memory = spec.Resources.MemoryMiB * 1024 * 1024
	}
	if spec.Resources.CPUShares > 0 {
		hostCfg.CPUShares = spec.Resources.CPUShares
	}

	netCfg := &network.NetworkingConfig{}

	created, err := m.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, spec.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: create %s: %w", ErrDaemonUnavailable, spec.Image, err)
	}
	if err := m.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		// Best-effort cleanup so a failed start doesn't leave a stopped
		// container on disk.
		_ = m.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("%w: start %s: %w", ErrDaemonUnavailable, created.ID, err)
	}

	info, err := m.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		// Container started but we can't read its name back. Surface the
		// error — the caller is free to use the ID we already have.
		return &Container{ID: created.ID}, fmt.Errorf("%w: inspect %s: %w", ErrDaemonUnavailable, created.ID, err)
	}
	return &Container{ID: created.ID, Name: strings.TrimPrefix(info.Name, "/")}, nil
}

// Stop sends a graceful stop signal (SIGTERM) and waits up to grace for
// the container to exit. If the timeout elapses Docker sends SIGKILL
// internally. Then the container is removed with Force=true so a stale
// stopped container can't leak across daemon restarts.
//
// A "no such container" reply is treated as success — the container is
// already gone and that's the state we wanted.
func (m *Manager) Stop(ctx context.Context, id string, grace time.Duration) error {
	if m == nil {
		return fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("containermgr: Stop requires a container id")
	}
	graceSecs := int(grace / time.Second)
	if graceSecs <= 0 {
		graceSecs = 10
	}
	if err := m.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &graceSecs}); err != nil {
		if !isNotFound(err) && !isRemovalInProgress(err) {
			return fmt.Errorf("%w: stop %s: %w", ErrDaemonUnavailable, id, err)
		}
	}
	// Force-remove so an AutoRemove==false container doesn't stick around.
	// With AutoRemove==true the Docker daemon may already be racing us
	// to remove the container — both "removal in progress" and "no such
	// container" are success states (the container is gone or will be).
	if err := m.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		if !isNotFound(err) && !isRemovalInProgress(err) {
			return fmt.Errorf("%w: remove %s: %w", ErrDaemonUnavailable, id, err)
		}
	}
	return nil
}

// isRemovalInProgress reports whether err is the Docker SDK's "removal
// in progress" message. Surfaces when AutoRemove and an explicit Stop
// race; treating it as success is correct because the post-state is
// what we wanted.
func isRemovalInProgress(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "removal of container") &&
		strings.Contains(err.Error(), "is already in progress")
}

// Inspect returns the live info for a container.
func (m *Manager) Inspect(ctx context.Context, id string) (*ContainerInfo, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	info, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("containermgr: container %s not found", id)
		}
		return nil, fmt.Errorf("%w: inspect %s: %w", ErrDaemonUnavailable, id, err)
	}
	created, _ := time.Parse(time.RFC3339Nano, info.Created)
	exit := 0
	if info.State != nil {
		exit = info.State.ExitCode
	}
	status := ""
	if info.State != nil {
		status = info.State.Status
	}
	return &ContainerInfo{
		ID:       info.ID,
		Name:     strings.TrimPrefix(info.Name, "/"),
		Image:    info.Image,
		Status:   status,
		Labels:   info.Config.Labels,
		Created:  created,
		ExitCode: exit,
	}, nil
}

// List returns every container matching f (running and stopped).
func (m *Manager) List(ctx context.Context, f Filter) ([]ContainerInfo, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	args := filters.NewArgs()
	for k, v := range f.Labels {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("%w: list: %w", ErrDaemonUnavailable, err)
	}
	out := make([]ContainerInfo, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, ContainerInfo{
			ID:      c.ID,
			Name:    name,
			Image:   c.Image,
			Status:  c.State,
			Labels:  c.Labels,
			Created: time.Unix(c.Created, 0).UTC(),
		})
	}
	return out, nil
}

// ForceRemoveByLabel removes every container with the given label. Used
// by tests' t.Cleanup to clear any container they spawned. Idempotent —
// returns nil if no containers match.
func (m *Manager) ForceRemoveByLabel(ctx context.Context, key, value string) error {
	if m == nil {
		return fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	args := filters.NewArgs()
	if value == "" {
		args.Add("label", key)
	} else {
		args.Add("label", key+"="+value)
	}
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return fmt.Errorf("containermgr: list by label %s=%s: %w", key, value, err)
	}
	var errs []error
	for _, c := range list {
		if err := m.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", c.ID, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ExecResult is the structured return of Exec — stdout, stderr, exit
// code. Stdout/Stderr are captured in-process; long-running execs are
// not the intended use case (the operator-level `sextant exec` is a
// one-shot tool).
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExecSpec is the input to Exec. WorkingDir and Env are optional.
type ExecSpec struct {
	Cmd        []string
	WorkingDir string
	Env        map[string]string
}

// Exec runs a one-shot command inside the supplied container and
// returns the captured stdout/stderr + exit code. Errors that the
// caller cares about (container missing, daemon unreachable) are
// wrapped with ErrDaemonUnavailable; a non-zero exit code is NOT an
// error — it's returned via ExecResult.ExitCode so the caller can
// surface it untouched (mirroring docker exec semantics).
//
// The implementation uses Docker's exec API: create the exec
// instance, attach to get stdout/stderr, inspect to read the exit
// code. The exec dies if ctx is canceled; the caller's deadline is
// the only timeout.
func (m *Manager) Exec(ctx context.Context, id string, spec ExecSpec) (ExecResult, error) {
	if m == nil {
		return ExecResult{}, fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if strings.TrimSpace(id) == "" {
		return ExecResult{}, fmt.Errorf("containermgr: Exec requires a container id")
	}
	if len(spec.Cmd) == 0 {
		return ExecResult{}, fmt.Errorf("containermgr: Exec requires a non-empty Cmd")
	}

	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	createResp, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          spec.Cmd,
		Env:          env,
		WorkingDir:   spec.WorkingDir,
	})
	if err != nil {
		if isNotFound(err) {
			return ExecResult{}, fmt.Errorf("containermgr: container %s not found", id)
		}
		return ExecResult{}, fmt.Errorf("%w: exec create %s: %w", ErrDaemonUnavailable, id, err)
	}

	hijack, err := m.cli.ContainerExecAttach(ctx, createResp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("%w: exec attach %s: %w", ErrDaemonUnavailable, id, err)
	}
	defer hijack.Close()

	// Docker multiplexes stdout/stderr on the same hijacked stream with
	// a leading 8-byte header per frame: {stream-id, 0, 0, 0, len[4]}.
	// We use stdcopy to demux — that's what the Docker CLI does
	// internally, so the formatting matches `docker exec` byte-for-byte.
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, hijack.Reader); err != nil {
		// Best-effort: return what we have plus the error.
		return ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, fmt.Errorf("containermgr: exec stream %s: %w", id, err)
	}

	inspect, err := m.cli.ContainerExecInspect(ctx, createResp.ID)
	if err != nil {
		return ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()},
			fmt.Errorf("%w: exec inspect %s: %w", ErrDaemonUnavailable, id, err)
	}
	return ExecResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: inspect.ExitCode,
	}, nil
}

// Logs returns the combined stdout+stderr of a container, tailing the
// last n lines. Used by tests when a spawn failure needs investigation.
func (m *Manager) Logs(ctx context.Context, id string, tail int) (string, error) {
	if m == nil {
		return "", fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	tailStr := "all"
	if tail > 0 {
		tailStr = fmt.Sprintf("%d", tail)
	}
	rc, err := m.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tailStr,
	})
	if err != nil {
		return "", fmt.Errorf("%w: logs %s: %w", ErrDaemonUnavailable, id, err)
	}
	defer rc.Close() //nolint:errcheck // best-effort close
	raw, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("containermgr: read logs %s: %w", id, err)
	}
	return string(raw), nil
}

// EnsureVolume creates a Docker named volume with the given name if it
// doesn't already exist, returning true when the volume was newly
// created (caller is expected to populate it) and false when it was
// already present.
//
// The labels are stamped onto the volume so operators can find sextant-
// owned volumes via `docker volume ls -f label=...`. Existing volumes
// are not relabeled — they keep whatever labels they were created with.
//
// Used by the claude_seed copy-on-spawn flow in spawn.go: a missing
// volume signals "first spawn, populate from host seed dir"; an
// existing volume signals "resume the previously-populated copy."
func (m *Manager) EnsureVolume(ctx context.Context, name string, labels map[string]string) (created bool, err error) {
	if m == nil {
		return false, fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if strings.TrimSpace(name) == "" {
		return false, fmt.Errorf("containermgr: EnsureVolume requires a name")
	}
	// Inspect first; if found we return false. VolumeInspect returns a
	// "no such volume" error when the volume is missing.
	if _, err := m.cli.VolumeInspect(ctx, name); err == nil {
		return false, nil
	} else if !isNotFound(err) && !isVolumeNotFound(err) {
		return false, fmt.Errorf("%w: inspect volume %s: %w", ErrDaemonUnavailable, name, err)
	}
	_, err = m.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Driver: "local",
		Labels: labels,
	})
	if err != nil {
		return false, fmt.Errorf("%w: create volume %s: %w", ErrDaemonUnavailable, name, err)
	}
	return true, nil
}

// RemoveVolume deletes a Docker named volume. Idempotent: a "no such
// volume" reply is treated as success. force=true asks Docker to delete
// the volume even if it's referenced by stopped containers.
func (m *Manager) RemoveVolume(ctx context.Context, name string, force bool) error {
	if m == nil {
		return fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("containermgr: RemoveVolume requires a name")
	}
	if err := m.cli.VolumeRemove(ctx, name, force); err != nil {
		if isNotFound(err) || isVolumeNotFound(err) {
			return nil
		}
		return fmt.Errorf("%w: remove volume %s: %w", ErrDaemonUnavailable, name, err)
	}
	return nil
}

// VolumeExists returns true if the named volume is present on the
// daemon. Used by tests and by the copy-on-spawn idempotency check.
func (m *Manager) VolumeExists(ctx context.Context, name string) (bool, error) {
	if m == nil {
		return false, fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if _, err := m.cli.VolumeInspect(ctx, name); err == nil {
		return true, nil
	} else if isNotFound(err) || isVolumeNotFound(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("%w: inspect volume %s: %w", ErrDaemonUnavailable, name, err)
	}
}

// PopulateVolumeFromHostDir copies the contents of hostSrc into the
// named volume by running a one-shot container that mounts both and
// invokes the supplied populate command. image is the image to use for
// the one-shot (the sidecar image is a safe choice — it's already
// pulled). The container is auto-removed on exit.
//
// The default command (when cmd is nil) is `sh -c "cp -a /src/. /dst/"`,
// which copies every entry (including dotfiles) from /src into /dst.
// Callers that need a different copy semantic supply cmd.
//
// Returns an error if the one-shot exits non-zero so a partial copy
// surfaces as a spawn failure rather than a silently broken volume.
func (m *Manager) PopulateVolumeFromHostDir(ctx context.Context, volumeName, hostSrc, image string, cmd []string) error {
	if m == nil {
		return fmt.Errorf("%w: nil manager", ErrDaemonUnavailable)
	}
	if strings.TrimSpace(volumeName) == "" {
		return fmt.Errorf("containermgr: PopulateVolumeFromHostDir requires a volume name")
	}
	if strings.TrimSpace(hostSrc) == "" {
		return fmt.Errorf("containermgr: PopulateVolumeFromHostDir requires a host source path")
	}
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("containermgr: PopulateVolumeFromHostDir requires an image")
	}
	if len(cmd) == 0 {
		// `cp -a /src/. /dst/` copies every entry from /src (including
		// hidden files) into /dst without recreating the /src dir
		// itself. The trailing `chown 1000:1000 /dst` re-owns the volume
		// to the sidecar image's `agent` user (uid 1000) so the spawned
		// agent container — which runs as uid 1000 — can write its
		// session journal into /home/agent/.claude/projects. Without the
		// chown the volume is root-owned (Docker named-volume default)
		// and the agent's writes fail with EPERM, reproducing the exact
		// symptom the bug-claude-seed-readonly-breaks-session-persistence
		// fix aims to eliminate.
		//
		// `set -e` so any of the three steps failing exits non-zero and
		// surfaces here.
		cmd = []string{"sh", "-c", "set -e; cp -a /src/. /dst/ && chown -R 1000:1000 /dst"}
	}
	// User=0:0 (root) so the populate one-shot can write to a fresh
	// named volume (default owner: root) AND set the final ownership to
	// the sidecar's `agent` user. The image's runtime USER directive is
	// ignored for this one-shot.
	cfg := &container.Config{
		Image:      image,
		Cmd:        cmd,
		Entrypoint: []string{}, // explicitly override the image's ENTRYPOINT (sidecar.sh) so plain cp -a runs
		User:       "0:0",
	}
	hostCfg := &container.HostConfig{
		AutoRemove: false, // we want to inspect exit code; explicit remove below
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: hostSrc, Target: "/src", ReadOnly: true},
			{Type: mount.TypeVolume, Source: volumeName, Target: "/dst"},
		},
	}
	created, err := m.cli.ContainerCreate(ctx, cfg, hostCfg, &network.NetworkingConfig{}, nil, "")
	if err != nil {
		return fmt.Errorf("%w: create populate container: %w", ErrDaemonUnavailable, err)
	}
	// Always remove the populate container, even on error paths.
	defer func() {
		_ = m.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true}) //nolint:contextcheck // best-effort cleanup
	}()
	if err := m.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("%w: start populate container: %w", ErrDaemonUnavailable, err)
	}
	statusCh, errCh := m.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("%w: wait populate container: %w", ErrDaemonUnavailable, err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			// Capture logs so the operator sees the cp error inline.
			logs, _ := m.Logs(ctx, created.ID, 50) //nolint:contextcheck // best-effort logs
			return fmt.Errorf("containermgr: populate volume %s exited with code %d: %s",
				volumeName, status.StatusCode, logs)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// isVolumeNotFound matches the Docker SDK's "no such volume" error
// shape. VolumeRemove uses a different sentinel from container ops, so
// we string-match both common forms the server returns.
func isVolumeNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "No such volume") || strings.Contains(s, "no such volume")
}

// isNotFound returns true for the Docker SDK's "no such container"
// error. The SDK doesn't export a typed sentinel reachable from this
// client version cleanly (cerrdefs is the v28+ home), so we string-
// match the stable substring that lives in errdefs.notFound.Error().
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if client.IsErrNotFound(err) { //nolint:staticcheck // cerrdefs.IsNotFound isn't reachable on the pinned SDK version
		return true
	}
	s := err.Error()
	return strings.Contains(s, "No such container") || strings.Contains(s, "no such container")
}
