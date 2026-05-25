package containermgr

import (
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
	"github.com/docker/docker/client"
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

// MountSpec is a single host-path → container-path bind mount.
type MountSpec struct {
	HostPath      string
	ContainerPath string
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
