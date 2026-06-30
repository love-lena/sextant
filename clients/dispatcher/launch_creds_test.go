package main

import (
	"strings"
	"testing"
)

// TASK-260 trust posture (AC#2 / AC#3): a sandboxed worker is launched CREDENTIAL-FREE —
// it carries NO git/gh push credentials, because opening a PR is the coordinator's
// host-side trusted step, never the jailed worker's. The dispatcher inherits its own
// environment into the worker (cmd.Env = append(os.Environ(), ...)), so a GH_TOKEN /
// SSH_AUTH_SOCK in the dispatcher's environment would leak into the worker WITHOUT the
// scrub. These tests assert the scrub strips them.

// TestScrubGitCreds_DropsCredentialVars is the unit property: scrubGitCreds removes every
// known git/gh credential carrier and keeps everything else.
func TestScrubGitCreds_DropsCredentialVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"GH_TOKEN=ghp_secret",
		"GITHUB_TOKEN=ghs_secret",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		"GIT_ASKPASS=/usr/bin/askpass",
		"GIT_SSH_COMMAND=ssh -i /key",
		"SEXTANT_STORE=/store",
		"ANTHROPIC_API_KEY=keep-me", // the worker NEEDS its model key — must survive
		"HOME=/home/op",
	}
	out := scrubGitCreds(in)
	dropped := []string{"GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "GIT_ASKPASS", "GIT_SSH_COMMAND"}
	for _, kv := range out {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		for _, d := range dropped {
			if name == d {
				t.Fatalf("scrubGitCreds kept credential var %q (it must be dropped from a worker's env)", name)
			}
		}
	}
	// The non-credential vars the worker needs must survive.
	for _, want := range []string{"PATH=/usr/bin", "SEXTANT_STORE=/store", "ANTHROPIC_API_KEY=keep-me", "HOME=/home/op"} {
		if !contains(out, want) {
			t.Fatalf("scrubGitCreds dropped a non-credential var %q (the worker needs it)", want)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestLaunchHarness_NoGitCredsInJail (AC#3, the no-creds-in-jail property): even when the
// DISPATCHER's own environment carries GH_TOKEN / GITHUB_TOKEN / SSH_AUTH_SOCK, the worker
// launchHarness starts sees NONE of them. RED reproduction: drop the scrubGitCreds call in
// launch() (revert to append(os.Environ(), ...)) and the worker inherits the tokens — this
// test then sees them and goes RED.
func TestLaunchHarness_NoGitCredsInJail(t *testing.T) {
	// Plant credentials in the dispatcher's (this process's) environment.
	t.Setenv("GH_TOKEN", "ghp_should_not_leak")
	t.Setenv("GITHUB_TOKEN", "ghs_should_not_leak")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/should-not-leak.sock")

	ag := &managedAgent{id: "agent-creds-1", nick: "creds", credsPath: "/dev/null", job: "job", model: DefaultModel}
	// Capture the credential-shaped env the launched worker actually sees.
	got := runHarnessCaptureEnv(t, ag, "GH_TOKEN\\|GITHUB_TOKEN\\|SSH_AUTH_SOCK")
	if got != "" {
		t.Fatalf("a sandboxed worker inherited git/gh credentials from the dispatcher env:\n%s\n"+
			"(the credential scrub failed — a jailed worker must carry NO push credentials; TASK-260 AC#3)", got)
	}
}

// (runHarnessCaptureEnv lives in workdir_test.go; it launches the dispatcher's harness for
// ag and returns the `env | grep PATTERN` line the harness captured, or "" if none.)
