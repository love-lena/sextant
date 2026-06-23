package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/sextant/internal/components"
)

// TestRunSecretSetAnthropicWritesEnv: `secret set anthropic` writes violet.env at
// $SEXTANT_HOME/violet.env, mode 0600, carrying ANTHROPIC_API_KEY=<value>.
func TestRunSecretSetAnthropicWritesEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)

	var out strings.Builder
	if err := runSecretSetAnthropic(&out, "sk-ant-secret"); err != nil {
		t.Fatalf("runSecretSetAnthropic: %v", err)
	}
	path := filepath.Join(home, "violet.env")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("violet.env not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("violet.env mode = %o, want 0600", perm)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "ANTHROPIC_API_KEY=sk-ant-secret") {
		t.Fatalf("violet.env contents = %q, want the key line", string(b))
	}
	// And LoadKeyEnv (the exec-path reader) reads it back.
	env, err := components.LoadKeyEnv(path)
	if err != nil || env[components.AnthropicKeyVar] != "sk-ant-secret" {
		t.Fatalf("round-trip via LoadKeyEnv failed: env=%v err=%v", env, err)
	}
}

// TestRunSecretSetAnthropicEmptyKey: an empty key writes nothing and fails loud.
func TestRunSecretSetAnthropicEmptyKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)
	var out strings.Builder
	if err := runSecretSetAnthropic(&out, ""); err == nil {
		t.Fatal("an empty key must fail loud")
	}
	if _, err := os.Stat(filepath.Join(home, "violet.env")); !os.IsNotExist(err) {
		t.Fatal("no env-file should be written for an empty key")
	}
}

// TestPromptSecretNonTTYReadsLine: with piped (non-TTY) input, promptSecret reads
// one line so the command stays scriptable, and prints the prompt.
func TestPromptSecretNonTTYReadsLine(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString("sk-piped-key\n")
		_ = w.Close()
	}()
	var out strings.Builder
	got, err := promptSecret(r, &out, "key: ")
	if err != nil {
		t.Fatalf("promptSecret: %v", err)
	}
	if got != "sk-piped-key" {
		t.Fatalf("promptSecret = %q, want sk-piped-key", got)
	}
	if !strings.Contains(out.String(), "key: ") {
		t.Fatalf("prompt not printed; out=%q", out.String())
	}
}

// TestStartComponentVioletKeylessFailsLoud: starting violet with no env-file
// fails loud (guiding to `secret set anthropic`) and writes NO plist — never
// start violet keyless. The bus is never contacted (the key pre-flight comes
// before identity minting), so this needs no launchd and no bus.
func TestStartComponentVioletKeylessFailsLoud(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)

	c, ok := components.Find("violet")
	if !ok {
		t.Fatal("violet must be registered")
	}
	// A Manager with no usable launchctl; startComponent must error at the key
	// pre-flight before it ever reaches launchd or the bus. The binary lookup
	// would also fail without sextant-violet on PATH, so seed a fake on PATH.
	bin := filepath.Join(t.TempDir(), "sextant-violet")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	mgr := &components.Manager{UID: 501, Home: home, Self: "/b/sextant", Run: func(...string) (string, error) { return "", nil }}
	err := startComponent(c, mgr, t.TempDir())
	if err == nil {
		t.Fatal("starting violet keyless must fail loud")
	}
	if !strings.Contains(err.Error(), "secret set anthropic") {
		t.Fatalf("error should guide to `secret set anthropic`; got %v", err)
	}
	if _, serr := os.Stat(components.PlistPath(home, "violet")); !os.IsNotExist(serr) {
		t.Fatal("no plist should be written when violet is keyless")
	}
}
