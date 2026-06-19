package components

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
)

// secret.go holds the env-file path for a NeedsKey component (violet) and the
// load that injects its key at exec time. The key is read from a 0600 file under
// $SEXTANT_HOME and set in the process environment by `components exec` JUST
// before the syscall.Exec — it is NEVER written into the launchd plist's
// EnvironmentVariables (a plist lands world-readable at 0644 under
// ~/Library/LaunchAgents, so a secret there would leak; the exec indirection is
// exactly the seam that lets us avoid that). sextant-violet's --api-key already
// defaults from $ANTHROPIC_API_KEY, so a loaded key flows straight through.

// AnthropicKeyVar is the env var sextant-violet's --api-key defaults from.
const AnthropicKeyVar = "ANTHROPIC_API_KEY"

// VioletEnvPath is the 0600 env-file holding violet's Anthropic key, under the
// client-config root ($SEXTANT_HOME). `sextant secret set anthropic` writes it;
// `components exec` reads it for a NeedsKey component just before the re-exec.
func VioletEnvPath() string { return filepath.Join(clictx.Root(), "violet.env") }

// LoadKeyEnv reads a NeedsKey component's env-file (KEY=VALUE lines) and returns
// its variables. It FAILS LOUD with operator guidance when the file is absent or
// carries no ANTHROPIC_API_KEY — violet must never start keyless. Used both by
// `components start` (as a pre-flight before writing a plist) and by
// `components exec` (to set the key in the environment before the re-exec).
func LoadKeyEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("violet needs an API key — run `sextant secret set anthropic` first (expected %s)", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if env[AnthropicKeyVar] == "" {
		return nil, fmt.Errorf("%s has no %s — run `sextant secret set anthropic` to set it", path, AnthropicKeyVar)
	}
	return env, nil
}

// WriteKeyEnv writes path with mode 0600 containing ANTHROPIC_API_KEY=<key>,
// creating its parent dir. The 0600 mode is the whole point — the key lives only
// in a file the operator owns, never in a plist or a process arg.
func WriteKeyEnv(path, key string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	content := AnthropicKeyVar + "=" + key + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
