package main

import (
	"flag"
	"strings"
	"testing"

	"github.com/love-lena/sextant/bus/buscfg"
)

// parseLeafFlag builds a FlagSet with the same leaf-listen flag `up` uses and
// parses args into it, so resolveLeafListen sees the real flag.Visit state.
func parseLeafFlag(t *testing.T, args []string) (*flag.FlagSet, string) {
	t.Helper()
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	leaf := fs.String("leaf-listen", "", "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return fs, *leaf
}

func TestResolveLeafListenPrecedence(t *testing.T) {
	const (
		flagAddr = "127.0.0.1:1111"
		envAddr  = "127.0.0.1:2222"
		cfgAddr  = "127.0.0.1:3333"
	)
	cases := []struct {
		name string
		args []string // up args (whether --leaf-listen was passed)
		env  string
		cfg  string
		want string
	}{
		{"unset is off", nil, "", "", ""},
		{"config only", nil, "", cfgAddr, cfgAddr},
		{"env only", nil, envAddr, "", envAddr},
		{"env overrides config", nil, envAddr, cfgAddr, envAddr},
		{"flag only", []string{"--leaf-listen", flagAddr}, "", "", flagAddr},
		{"flag overrides config", []string{"--leaf-listen", flagAddr}, "", cfgAddr, flagAddr},
		{"flag overrides env", []string{"--leaf-listen", flagAddr}, envAddr, "", flagAddr},
		{"flag overrides both", []string{"--leaf-listen", flagAddr}, envAddr, cfgAddr, flagAddr},
		// An explicit empty flag is a deliberate override that wins over env/config.
		{"explicit empty flag wins", []string{"--leaf-listen", ""}, envAddr, cfgAddr, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, flagVal := parseLeafFlag(t, tc.args)
			got := resolveLeafListen(fs, flagVal, tc.env, tc.cfg)
			if got != tc.want {
				t.Errorf("resolveLeafListen = %q, want %q", got, tc.want)
			}
		})
	}
}

// parsePortFlag builds a FlagSet with the same --port flag `up` uses and parses
// args into it, so resolvePort sees the real flag.Visit state.
func parsePortFlag(t *testing.T, args []string) (*flag.FlagSet, int) {
	t.Helper()
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	port := fs.Int("port", 0, "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return fs, *port
}

func TestResolvePortPrecedence(t *testing.T) {
	cases := []struct {
		name string
		args []string // up args (whether --port was passed)
		cfg  int
		want int
	}{
		{"unset is auto", nil, 0, 0},
		{"config only", nil, 63527, 63527},
		{"flag only", []string{"--port", "4222"}, 0, 4222},
		{"flag overrides config", []string{"--port", "4222"}, 63527, 4222},
		// An explicit --port=0 is a deliberate override (force auto) over a config pin.
		{"explicit zero flag wins", []string{"--port", "0"}, 63527, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, flagVal := parsePortFlag(t, tc.args)
			if got := resolvePort(fs, flagVal, tc.cfg); got != tc.want {
				t.Errorf("resolvePort = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRunConfigSetWritesLeafListen(t *testing.T) {
	store := t.TempDir()
	var out strings.Builder
	if err := runConfigSet(&out, store, []string{"leaf-listen", "127.0.0.1:7422"}); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	cfg, err := buscfg.Load(buscfg.Path(store))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LeafListen != "127.0.0.1:7422" {
		t.Errorf("LeafListen = %q, want 127.0.0.1:7422", cfg.LeafListen)
	}
	if !strings.Contains(out.String(), "brew services restart") {
		t.Errorf("set output should point at the restart step; got %q", out.String())
	}
}

func TestRunConfigSetClearLeafListen(t *testing.T) {
	store := t.TempDir()
	if err := buscfg.Save(buscfg.Path(store), buscfg.Config{LeafListen: "127.0.0.1:7422"}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runConfigSet(&out, store, []string{"leaf-listen", ""}); err != nil {
		t.Fatalf("runConfigSet clear: %v", err)
	}
	cfg, _ := buscfg.Load(buscfg.Path(store))
	if cfg.LeafListen != "" {
		t.Errorf("LeafListen = %q after clear, want empty", cfg.LeafListen)
	}
}

func TestRunConfigSetRejectsUnknownKey(t *testing.T) {
	var out strings.Builder
	err := runConfigSet(&out, t.TempDir(), []string{"bogus", "x"})
	if err == nil {
		t.Fatal("want error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "leaf-listen") || !strings.Contains(err.Error(), "port") {
		t.Errorf("error should name the settable keys; got %v", err)
	}
}

func TestRunConfigSetWritesPort(t *testing.T) {
	store := t.TempDir()
	var out strings.Builder
	if err := runConfigSet(&out, store, []string{"port", "63527"}); err != nil {
		t.Fatalf("runConfigSet port: %v", err)
	}
	cfg, err := buscfg.Load(buscfg.Path(store))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 63527 {
		t.Errorf("Port = %d, want 63527", cfg.Port)
	}
	if !strings.Contains(out.String(), "brew services restart") {
		t.Errorf("set output should point at the restart step; got %q", out.String())
	}
}

func TestRunConfigSetPortPreservesLeafListen(t *testing.T) {
	// Setting one key must not clobber the other already on disk.
	store := t.TempDir()
	if err := buscfg.Save(buscfg.Path(store), buscfg.Config{LeafListen: "127.0.0.1:7422"}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runConfigSet(&out, store, []string{"port", "63527"}); err != nil {
		t.Fatalf("runConfigSet port: %v", err)
	}
	cfg, _ := buscfg.Load(buscfg.Path(store))
	if cfg.Port != 63527 || cfg.LeafListen != "127.0.0.1:7422" {
		t.Errorf("set port clobbered leaf-listen: %+v", cfg)
	}
}

func TestRunConfigSetRejectsBadPort(t *testing.T) {
	var out strings.Builder
	for _, bad := range []string{"99999", "-1", "abc", ""} {
		if err := runConfigSet(&out, t.TempDir(), []string{"port", bad}); err == nil {
			t.Errorf("port %q: want error, got nil", bad)
		}
	}
}

func TestRunConfigGetPort(t *testing.T) {
	store := t.TempDir()
	if err := buscfg.Save(buscfg.Path(store), buscfg.Config{Port: 63527}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runConfigGet(&out, store, []string{"port"}); err != nil {
		t.Fatalf("runConfigGet port: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "63527" {
		t.Errorf("get port = %q, want 63527", got)
	}
}

func TestRunConfigSetUsageError(t *testing.T) {
	var out strings.Builder
	if err := runConfigSet(&out, t.TempDir(), []string{"leaf-listen"}); err == nil {
		t.Fatal("want usage error for missing value, got nil")
	}
}

func TestRunConfigGet(t *testing.T) {
	store := t.TempDir()
	if err := buscfg.Save(buscfg.Path(store), buscfg.Config{LeafListen: "127.0.0.1:7422"}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runConfigGet(&out, store, []string{"leaf-listen"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "127.0.0.1:7422" {
		t.Errorf("get leaf-listen = %q, want 127.0.0.1:7422", got)
	}
}

func TestRunConfigGetMissingIsUnset(t *testing.T) {
	// No config file: get must succeed and report unset (default-off), not error.
	var out strings.Builder
	if err := runConfigGet(&out, t.TempDir(), nil); err != nil {
		t.Fatalf("runConfigGet(missing): %v", err)
	}
	if !strings.Contains(out.String(), "unset") {
		t.Errorf("get with no config should report unset; got %q", out.String())
	}
}
