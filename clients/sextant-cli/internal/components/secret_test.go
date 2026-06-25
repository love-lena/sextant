package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVioletRegisteredNeedsKey: violet is a managed component, an agent, and is
// flagged NeedsKey (so the key pre-flight + the exec-time key load apply). Its
// launch args carry --designate (take the assistant designation) and never a
// secret.
func TestVioletRegisteredNeedsKey(t *testing.T) {
	c, ok := Find("violet")
	if !ok {
		t.Fatal("violet must be registered")
	}
	if c.Binary != "sextant-violet" || c.Kind != "agent" || !c.NeedsKey {
		t.Fatalf("violet entry wrong: %+v", c)
	}
	args := c.Args("creds.path", "store.dir", "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--creds creds.path") || !strings.Contains(joined, "--store store.dir") {
		t.Fatalf("violet args missing creds/store: %v", args)
	}
	if !strings.Contains(joined, "--designate") {
		t.Fatalf("violet args should carry --designate: %v", args)
	}
	if strings.Contains(joined, "api-key") || strings.Contains(joined, "ANTHROPIC") {
		t.Fatalf("violet args must NOT carry a key (it rides the env-file): %v", args)
	}
}

// TestWriteThenLoadKeyEnv: WriteKeyEnv writes a 0600 file carrying the key, and
// LoadKeyEnv reads it back with ANTHROPIC_API_KEY set.
func TestWriteThenLoadKeyEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "violet.env")
	if err := WriteKeyEnv(path, "sk-ant-test-123"); err != nil {
		t.Fatalf("WriteKeyEnv: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("env-file mode = %o, want 0600 (a secret must not be world-readable)", perm)
	}
	env, err := LoadKeyEnv(path)
	if err != nil {
		t.Fatalf("LoadKeyEnv: %v", err)
	}
	if env[AnthropicKeyVar] != "sk-ant-test-123" {
		t.Fatalf("loaded key = %q, want the written value", env[AnthropicKeyVar])
	}
}

// TestLoadKeyEnvMissingFile: an absent env-file fails loud with the operator
// guidance to run `sextant secret set anthropic`.
func TestLoadKeyEnvMissingFile(t *testing.T) {
	_, err := LoadKeyEnv(filepath.Join(t.TempDir(), "nope.env"))
	if err == nil {
		t.Fatal("a missing env-file must fail loud")
	}
	if !strings.Contains(err.Error(), "secret set anthropic") {
		t.Fatalf("error should guide to `secret set anthropic`; got %v", err)
	}
}

// TestLoadKeyEnvNoKey: a present file with no ANTHROPIC_API_KEY fails loud —
// violet must never start keyless.
func TestLoadKeyEnvNoKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "violet.env")
	if err := os.WriteFile(path, []byte("# a comment\nOTHER=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadKeyEnv(path)
	if err == nil {
		t.Fatal("an env-file with no ANTHROPIC_API_KEY must fail loud")
	}
	if !strings.Contains(err.Error(), AnthropicKeyVar) {
		t.Fatalf("error should name %s; got %v", AnthropicKeyVar, err)
	}
}

// TestLoadKeyEnvSkipsCommentsAndBlanks: comments and blank lines are ignored, and
// surrounding whitespace on a KEY=VALUE line is trimmed.
func TestLoadKeyEnvSkipsCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "violet.env")
	body := "# header\n\n  ANTHROPIC_API_KEY = sk-spaced  \nEXTRA=ok\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := LoadKeyEnv(path)
	if err != nil {
		t.Fatalf("LoadKeyEnv: %v", err)
	}
	if env[AnthropicKeyVar] != "sk-spaced" {
		t.Fatalf("key = %q, want trimmed sk-spaced", env[AnthropicKeyVar])
	}
	if env["EXTRA"] != "ok" {
		t.Fatalf("EXTRA = %q, want ok", env["EXTRA"])
	}
}

// TestVioletEnvPathUnderRoot: the env-file sits at $SEXTANT_HOME/violet.env.
func TestVioletEnvPathUnderRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)
	if got, want := VioletEnvPath(), filepath.Join(home, "violet.env"); got != want {
		t.Fatalf("VioletEnvPath = %q, want %q", got, want)
	}
}
