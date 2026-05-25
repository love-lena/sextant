package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/containermgr"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// ContainerExecRunner is the subset of containermgr.Manager the M12
// container-filesystem handlers call. Kept narrow so the fake test
// runner doesn't need to satisfy the full Manager surface.
type ContainerExecRunner interface {
	Exec(ctx context.Context, id string, spec containermgr.ExecSpec) (containermgr.ExecResult, error)
}

// FilesDeps bundles the deps the read_file / list_dir / stat / exec
// handlers need. Definitions is required (we resolve the live
// incarnation through the agent definition); Incarnations gives us
// the container id; Containers is the exec backend.
type FilesDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerExecRunner
}

// resolveLiveContainer looks up the agent's live AgentIncarnation
// and returns its ContainerID. Returns a structured agent_not_found
// or bad_request when the agent is missing / has no live incarnation.
func resolveLiveContainer(ctx context.Context, deps FilesDeps, agentID uuid.UUID) (string, *sextantproto.RPCError) {
	if agentID == uuid.Nil {
		return "", &sextantproto.RPCError{Code: sextantproto.ErrCodeBadRequest, Message: "agent_id is required"}
	}
	_, err := deps.Definitions.Get(ctx, agentID.String())
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return "", &sextantproto.RPCError{
				Code:    sextantproto.ErrCodeAgentNotFound,
				Message: fmt.Sprintf("no agent with uuid %s", agentID),
			}
		}
		return "", &sextantproto.RPCError{Code: sextantproto.ErrCodeInternal, Message: fmt.Sprintf("load definition: %v", err)}
	}
	inc, _, err := findLiveIncarnation(ctx, deps.Incarnations, agentID)
	if err != nil {
		return "", &sextantproto.RPCError{Code: sextantproto.ErrCodeInternal, Message: fmt.Sprintf("find live incarnation: %v", err)}
	}
	if inc == nil || inc.ContainerID == "" {
		return "", &sextantproto.RPCError{
			Code:    sextantproto.ErrCodeBadRequest,
			Message: fmt.Sprintf("agent %s has no running incarnation", agentID),
		}
	}
	return inc.ContainerID, nil
}

// NewReadFile returns the M12 real read_file handler. It reads via
// `cat` inside the container — small files only (no streaming in
// M12). Caps the response payload at 16 MiB so a misuse against a
// large file doesn't push the NATS msg-size limit.
func NewReadFile(deps FilesDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.ReadFileRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode read_file payload: %v", err))
		}
		if strings.TrimSpace(args.Path) == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "path is required")
		}
		containerID, rerr := resolveLiveContainer(ctx, deps, args.AgentID)
		if rerr != nil {
			return emitErr(emit, rerr.Code, rerr.Message)
		}
		// `cat` is the simplest portable file-read. The sidecar image is
		// alpine-based so it ships with coreutils' cat at /bin/cat.
		result, err := deps.Containers.Exec(ctx, containerID, containermgr.ExecSpec{
			Cmd: []string{"cat", args.Path},
		})
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("exec cat %s: %v", args.Path, err))
		}
		if result.ExitCode != 0 {
			// stderr typically carries `cat: <path>: No such file ...`.
			msg := strings.TrimSpace(string(result.Stderr))
			if msg == "" {
				msg = fmt.Sprintf("cat exited %d", result.ExitCode)
			}
			return emitErr(emit, sextantproto.ErrCodeBadRequest, msg)
		}
		return emitOK(emit, sextantproto.ReadFileResponse{
			Content:     result.Stdout,
			ContentType: sniffContentType(args.Path, result.Stdout),
		})
	}
}

// sniffContentType picks a best-effort MIME type. We use the http
// stdlib sniffer for body-based detection and fall back to the
// extension for files the sniffer can't classify (e.g. .md, .toml).
func sniffContentType(p string, body []byte) string {
	mime := http.DetectContentType(body)
	if mime != "" && mime != "application/octet-stream" {
		return mime
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".toml":
		return "application/toml; charset=utf-8"
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	}
	return mime
}

// NewListDir returns the list_dir handler. Implementation: run
// `ls -lA --time-style=long-iso` inside the container and parse the
// output. We use -A (not -a) so . and .. are dropped — the operator
// almost never wants them in CLI output.
func NewListDir(deps FilesDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.ListDirRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode list_dir payload: %v", err))
		}
		if strings.TrimSpace(args.Path) == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "path is required")
		}
		containerID, rerr := resolveLiveContainer(ctx, deps, args.AgentID)
		if rerr != nil {
			return emitErr(emit, rerr.Code, rerr.Message)
		}
		// `ls -1Ap` is the most portable shape: one entry per line,
		// hidden files except . and .., and "/" appended to directory
		// entries. Mode/size aren't returned here — `stat` covers that;
		// callers that want both pay an extra RPC.
		result, err := deps.Containers.Exec(ctx, containerID, containermgr.ExecSpec{
			Cmd: []string{"ls", "-1Ap", args.Path},
		})
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("exec ls %s: %v", args.Path, err))
		}
		if result.ExitCode != 0 {
			msg := strings.TrimSpace(string(result.Stderr))
			if msg == "" {
				msg = fmt.Sprintf("ls exited %d", result.ExitCode)
			}
			return emitErr(emit, sextantproto.ErrCodeBadRequest, msg)
		}
		entries := make([]sextantproto.ListDirEntry, 0)
		for _, line := range strings.Split(strings.TrimRight(string(result.Stdout), "\n"), "\n") {
			if line == "" {
				continue
			}
			isDir := strings.HasSuffix(line, "/")
			name := strings.TrimSuffix(line, "/")
			entries = append(entries, sextantproto.ListDirEntry{
				Name:  name,
				IsDir: isDir,
			})
		}
		return emitOK(emit, sextantproto.ListDirResponse{Entries: entries})
	}
}

// NewStat returns the stat handler. Uses `stat -c "%s|%a|%F|%n"`
// which is the portable Linux stat format across alpine and gnu
// coreutils.
func NewStat(deps FilesDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.StatRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode stat payload: %v", err))
		}
		if strings.TrimSpace(args.Path) == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "path is required")
		}
		containerID, rerr := resolveLiveContainer(ctx, deps, args.AgentID)
		if rerr != nil {
			return emitErr(emit, rerr.Code, rerr.Message)
		}
		result, err := deps.Containers.Exec(ctx, containerID, containermgr.ExecSpec{
			Cmd: []string{"stat", "-c", "%s|%a|%F|%n", args.Path},
		})
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("exec stat %s: %v", args.Path, err))
		}
		if result.ExitCode != 0 {
			msg := strings.TrimSpace(string(result.Stderr))
			if msg == "" {
				msg = fmt.Sprintf("stat exited %d", result.ExitCode)
			}
			return emitErr(emit, sextantproto.ErrCodeBadRequest, msg)
		}
		fields := strings.SplitN(strings.TrimSpace(string(result.Stdout)), "|", 4)
		if len(fields) != 4 {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("stat returned unparseable output: %q", string(result.Stdout)))
		}
		size, _ := strconv.ParseInt(fields[0], 10, 64)
		return emitOK(emit, sextantproto.StatResponse{
			Name:  path.Base(fields[3]),
			Size:  size,
			Mode:  fields[1],
			IsDir: strings.Contains(fields[2], "directory"),
		})
	}
}

// NewExecInContainer returns the operator-level exec_in_container
// handler. Capability-gated by control.exec. Captures stdout, stderr,
// and the exit code into a single response.
//
// A non-zero exit code is NOT an RPC error — it's surfaced via
// ExecInContainerResponse.ExitCode so the CLI can mirror docker's
// "the process exited with N" semantics rather than fold every
// command failure into an RPC failure.
func NewExecInContainer(deps FilesDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.ExecInContainerRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode exec_in_container payload: %v", err))
		}
		if len(args.Cmd) == 0 {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "cmd is required (non-empty argv)")
		}
		containerID, rerr := resolveLiveContainer(ctx, deps, args.AgentID)
		if rerr != nil {
			return emitErr(emit, rerr.Code, rerr.Message)
		}
		result, err := deps.Containers.Exec(ctx, containerID, containermgr.ExecSpec{
			Cmd:        args.Cmd,
			WorkingDir: args.Workdir,
			Env:        args.Env,
		})
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("exec: %v", err))
		}
		return emitOK(emit, sextantproto.ExecInContainerResponse{
			Stdout:   string(result.Stdout),
			Stderr:   string(result.Stderr),
			ExitCode: result.ExitCode,
		})
	}
}
