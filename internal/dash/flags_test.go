package dash

import (
	"flag"
	"strings"
	"testing"
)

// parseFlags is the test seam: register the dash flags on a throwaway flag set,
// parse argv, and resolve. It mirrors exactly what cmd/sextant-dash and the
// `sextant dash` alias do.
func parseFlags(t *testing.T, argv ...string) (Options, error) {
	t.Helper()
	fs := flag.NewFlagSet("dash-test", flag.ContinueOnError)
	f := AddFlags(fs)
	if err := fs.Parse(argv); err != nil {
		t.Fatalf("parse %v: %v", argv, err)
	}
	return f.Resolve()
}

// hermeticEnv strips the developer's own SEXTANT_* vars and pins SEXTANT_HOME to
// an empty temp dir, so identity resolution sees no ambient creds/context and a
// no-identity case is genuinely no-identity (not the operator's real config).
func hermeticEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SEXTANT_CREDS", "SEXTANT_CONTEXT", "SEXTANT_STORE"} {
		t.Setenv(k, "")
	}
	t.Setenv("SEXTANT_HOME", t.TempDir())
}

// TestResolveRejectsBadTheme is review item 3: --theme must fail loud on an
// unknown value rather than silently falling back to auto.
func TestResolveRejectsBadTheme(t *testing.T) {
	hermeticEnv(t)
	_, err := parseFlags(t, "--creds", "/tmp/x.creds", "--theme", "purple")
	if err == nil {
		t.Fatal("--theme purple resolved without error; want a loud failure")
	}
	if !strings.Contains(err.Error(), "invalid --theme") || !strings.Contains(err.Error(), "purple") {
		t.Fatalf("error %q should name the bad value and the flag", err)
	}
}

// TestResolveAcceptsKnownThemes confirms the three documented values (and the
// auto default) resolve cleanly when an identity is present.
func TestResolveAcceptsKnownThemes(t *testing.T) {
	hermeticEnv(t)
	for _, th := range []string{"light", "dark", "auto"} {
		opts, err := parseFlags(t, "--creds", "/tmp/x.creds", "--theme", th)
		if err != nil {
			t.Fatalf("--theme %s: %v", th, err)
		}
		if string(opts.Theme) != th {
			t.Fatalf("--theme %s resolved to %q", th, opts.Theme)
		}
	}
	// The default (no --theme) is auto.
	opts, err := parseFlags(t, "--creds", "/tmp/x.creds")
	if err != nil {
		t.Fatalf("default theme: %v", err)
	}
	if opts.Theme != ThemeAuto {
		t.Fatalf("default --theme = %q, want auto", opts.Theme)
	}
}

// TestResolveNoIdentityDefersToFirstRun: with no creds, no context env, and no
// active context, Resolve does NOT fail — it returns Options with an empty
// CredsPath so Run can attempt the zero-config first run (self-enroll against a
// discoverable local bus, ADR-0024). A context named EXPLICITLY but missing is
// still a loud error (the operator asked for something that isn't there).
func TestResolveNoIdentityDefersToFirstRun(t *testing.T) {
	hermeticEnv(t)
	opts, err := parseFlags(t) // no --creds, no context
	if err != nil {
		t.Fatalf("Resolve with no identity should defer to the first-run path, got %v", err)
	}
	if opts.CredsPath != "" {
		t.Fatalf("CredsPath = %q, want empty (no identity resolved)", opts.CredsPath)
	}

	// An explicit --context that doesn't exist fails loud.
	if _, err := parseFlags(t, "--context", "nope"); err == nil {
		t.Fatal("an explicitly named missing context should be a loud error")
	}
}

// TestResolveNameFlag: --name threads through to Options.Name (the first-run
// enrollment display-name override).
func TestResolveNameFlag(t *testing.T) {
	hermeticEnv(t)
	opts, err := parseFlags(t, "--name", "captain")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Name != "captain" {
		t.Fatalf("Options.Name = %q, want captain", opts.Name)
	}
}
