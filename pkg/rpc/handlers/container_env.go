package handlers

import (
	"os"

	"github.com/google/uuid"
)

// containerEnvInput bundles the per-spawn / per-restart inputs to
// buildContainerEnv. Both handlers used to assemble this map inline
// with slightly different shapes (the restart path notably skipped
// ANTHROPIC_API_KEY, SEXTANT_MODEL, SEXTANT_PERMISSION_MODE and
// SEXTANT_SESSION_ID); see [[bug-restart-no-api-key-forwarding]] and
// [[bug-restart-preserve-session-noop]].
//
// We use a struct instead of a long positional signature so each call
// site stays self-documenting — restart in particular has to be
// explicit about whether the session id is being preserved.
type containerEnvInput struct {
	AgentUUID      uuid.UUID
	AgentName      string
	IncarnationID  uuid.UUID
	HostID         string
	NATSURL        string
	NATSUser       string
	NATSPassword   string
	JWT            string
	MCPURL         string
	Model          string
	PermissionMode string
	// APIKey, when non-empty, becomes ANTHROPIC_API_KEY. Empty means
	// "fall back to the SDK's default credential chain" (e.g. the
	// operator's local `claude` CLI login on macOS).
	APIKey string
	// SessionID, when non-empty, becomes SEXTANT_SESSION_ID. Restart
	// only sets this when --preserve-session is true; spawn sets it
	// from def.Runtime.SessionID iff a prior session was recorded.
	SessionID string
	// EnvOverlay is applied last and can override any of the well-
	// known SEXTANT_* keys. Spawn passes tpl.Env; restart passes
	// def.Sandbox.Env (which is cloned from tpl.Env at spawn time).
	EnvOverlay map[string]string
}

// buildContainerEnv constructs the env-var map handed to the sidecar
// container. The shape mirrors specs/components/sidecar-image.md
// §"Env vars" exactly and is shared between spawn_agent and
// restart_agent so the two paths can't drift again.
func buildContainerEnv(in containerEnvInput) map[string]string {
	env := map[string]string{
		"SEXTANT_AGENT_UUID":      in.AgentUUID.String(),
		"SEXTANT_AGENT_NAME":      in.AgentName,
		"SEXTANT_INCARNATION_ID":  in.IncarnationID.String(),
		"SEXTANT_HOST_ID":         in.HostID,
		"SEXTANT_NATS_URL":        in.NATSURL,
		"SEXTANT_NATS_USER":       in.NATSUser,
		"SEXTANT_NATS_PASSWORD":   in.NATSPassword,
		"SEXTANT_JWT":             in.JWT,
		"SEXTANT_MCP_URL":         in.MCPURL,
		"SEXTANT_MODEL":           in.Model,
		"SEXTANT_PERMISSION_MODE": in.PermissionMode,
	}
	if in.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = in.APIKey
	}
	if in.SessionID != "" {
		env["SEXTANT_SESSION_ID"] = in.SessionID
	}
	// EnvOverlay applied last so a template's `env` block *can*
	// override any of the well-known SEXTANT_* vars. Production
	// templates don't; the mock-driver tests do.
	for k, v := range in.EnvOverlay {
		env[k] = v
	}
	return env
}

// hostAPIKey returns ANTHROPIC_API_KEY from the daemon's own
// environment, or "" when unset. Centralized so spawn and restart
// pull the same string from the same source.
func hostAPIKey() string {
	return os.Getenv("ANTHROPIC_API_KEY")
}
