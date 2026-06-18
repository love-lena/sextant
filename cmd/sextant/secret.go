package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/love-lena/sextant/internal/components"
	"golang.org/x/term"
)

// `sextant secret set anthropic` stores violet's Anthropic API key in a 0600
// env-file ($SEXTANT_HOME/violet.env) that `sextant components exec violet` reads
// at launch time. The key is NEVER written into violet's launchd plist (a plist
// lands world-readable under ~/Library/LaunchAgents) — the env-file is the only
// place it lives, and the exec indirection sets it in the environment just before
// re-execing sextant-violet (whose --api-key defaults from $ANTHROPIC_API_KEY).
//
//	sextant secret set anthropic    prompt (no echo) + write violet.env 0600
//
// If violet's service is already installed, it is restarted so it picks up the
// new key.
func cmdSecret(args []string) {
	if len(args) == 0 {
		fatal("secret: expected a subcommand (set) — see `sextant help`")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "set":
		cmdSecretSet(rest)
	default:
		fatal("secret: unknown subcommand %q (want set)", sub)
	}
}

func cmdSecretSet(args []string) {
	fs := flag.NewFlagSet("secret set", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		fatal("secret set: usage: sextant secret set anthropic")
	}
	if rest[0] != "anthropic" {
		fatal("secret set: unknown secret %q (want anthropic)", rest[0])
	}

	key, err := promptSecret(os.Stdin, os.Stdout, "Anthropic API key (input hidden): ")
	if err != nil {
		fatal("%v", err)
	}
	if err := runSecretSetAnthropic(os.Stdout, key); err != nil {
		fatal("%v", err)
	}
	// If violet's service is already installed, restart it so it picks up the key
	// from the env-file we just wrote. A not-installed service is a no-op (the key
	// is in place for the next `sextant components start violet`).
	restartVioletIfPresent(*store)
}

// runSecretSetAnthropic is the testable core: validate the key is non-empty and
// write it to the 0600 env-file. It does not touch launchd (the CLI wrapper does
// the optional restart) so a test drives it without a real service.
func runSecretSetAnthropic(stdout io.Writer, key string) error {
	if key == "" {
		return fmt.Errorf("secret set anthropic: empty key — nothing written")
	}
	path := components.VioletEnvPath()
	if err := components.WriteKeyEnv(path, key); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (0600) — violet will use this key on its next start\n", path)
	return nil
}

// promptSecret reads a secret from the terminal WITHOUT echoing it. When stdin is
// a terminal it uses term.ReadPassword; otherwise (a pipe, e.g. in a test) it
// reads a single line so the command is still scriptable. A trailing newline is
// printed after the hidden read so the cursor moves on.
func promptSecret(in *os.File, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	if term.IsTerminal(int(in.Fd())) {
		b, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return string(b), nil
	}
	// Non-TTY (piped) input: read one line.
	var line string
	if _, err := fmt.Fscanln(in, &line); err != nil && err != io.EOF {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return line, nil
}

// restartVioletIfPresent restarts violet's managed service iff it is already
// loaded, so a fresh key is picked up. A not-installed service, or a non-macOS
// host, is a quiet no-op.
func restartVioletIfPresent(store string) {
	if !components.Supported() {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	mgr, err := components.NewManager(self)
	if err != nil {
		return
	}
	st, err := mgr.Status("violet")
	if err != nil || !st.Loaded {
		return
	}
	c, ok := components.Find("violet")
	if !ok {
		return
	}
	fmt.Println("  violet service is running — restarting to pick up the new key")
	if err := mgr.Stop(os.Stdout, "violet"); err == nil {
		if serr := startComponent(c, mgr, store); serr != nil {
			fmt.Fprintf(os.Stderr, "sextant: restart violet: %v\n", serr)
		}
	}
}
