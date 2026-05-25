package handlers

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

// TestBuildContainerEnvIncludesInitialPromptWhenSet pins the
// bug-initial-prompt-not-forwarded-to-sdk fix: when the template's
// initial_prompt is non-empty, buildContainerEnv must inject
// SEXTANT_INITIAL_PROMPT (base64-encoded) so the sidecar can decode it
// and hand the charter to the SDK as `systemPrompt`. Multi-line input
// is the realistic case — TOML triple-quoted charters span paragraphs.
func TestBuildContainerEnvIncludesInitialPromptWhenSet(t *testing.T) {
	t.Parallel()

	prompt := "You are the assistant agent.\nOperator is Lena Hickson.\n"
	env := buildContainerEnv(containerEnvInput{
		AgentUUID:     uuid.New(),
		AgentName:     "smoke",
		IncarnationID: uuid.New(),
		HostID:        "host-test",
		NATSURL:       "nats://localhost:4222",
		NATSUser:      "u",
		NATSPassword:  "p",
		JWT:           "jwt",
		Model:         "claude-opus-4-7[1m]",
		InitialPrompt: prompt,
	})

	raw, ok := env["SEXTANT_INITIAL_PROMPT"]
	if !ok {
		t.Fatalf("SEXTANT_INITIAL_PROMPT not set; got keys %v", keysOf(env))
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("SEXTANT_INITIAL_PROMPT not valid base64: %v (raw=%q)", err, raw)
	}
	if string(decoded) != prompt {
		t.Fatalf("decoded SEXTANT_INITIAL_PROMPT = %q, want %q", string(decoded), prompt)
	}
}

// TestBuildContainerEnvOmitsInitialPromptWhenEmpty confirms the env
// var is left unset rather than set to an empty string when the
// template has no initial_prompt — the sidecar's decode path branches
// on presence, not on length, so a blank var would log a misleading
// "initial_prompt loaded length=0".
func TestBuildContainerEnvOmitsInitialPromptWhenEmpty(t *testing.T) {
	t.Parallel()

	env := buildContainerEnv(containerEnvInput{
		AgentUUID:     uuid.New(),
		AgentName:     "smoke",
		IncarnationID: uuid.New(),
		HostID:        "host-test",
		NATSURL:       "nats://localhost:4222",
		NATSUser:      "u",
		NATSPassword:  "p",
		JWT:           "jwt",
		Model:         "claude-opus-4-7[1m]",
		InitialPrompt: "",
	})

	if _, ok := env["SEXTANT_INITIAL_PROMPT"]; ok {
		t.Fatalf("SEXTANT_INITIAL_PROMPT unexpectedly set when InitialPrompt empty; env=%v", env)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
