package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
	"github.com/love-lena/sextant/clients/go/apps/internal/dashserve"
)

// cmdDash is the `sextant dash` alias. The browser dash is THE dash now
// (ADR-0046), so this verb RESOLVES and OPENS the running web dash rather than
// serving it: serving is the sextant-dash binary's job (run as a managed
// component), and the terminal UI is reached via the separate sextant-tui binary.
//
// `sextant dash` (no subcommand) reads the managed-dash state file
// ($SEXTANT_HOME/dash.json), prints the URL, and best-effort opens it in the
// browser. `sextant dash url` prints the URL only. Both fail loud when the web
// dash is not running.
func cmdDash(args []string) {
	if len(args) > 0 && args[0] == "url" {
		cmdDashURL(args[1:])
		return
	}

	fs := flag.NewFlagSet("dash", flag.ExitOnError)
	_ = fs.Parse(args)

	state := readDashState()
	// Always print the URL (the source of truth); the browser open is a
	// convenience on top, so an opener failure never hides the URL.
	fmt.Println(state.URL)
	if err := openInBrowser(state.URL); err != nil {
		fmt.Fprintf(os.Stderr, "sextant dash: could not open a browser (%v) — open the URL above yourself\n", err)
	}
}

// cmdDashURL implements `sextant dash url`: reads the managed-dash state file
// ($SEXTANT_HOME/dash.json) and prints the URL.
func cmdDashURL(args []string) {
	fs := flag.NewFlagSet("dash url", flag.ExitOnError)
	_ = fs.Parse(args)
	fmt.Println(readDashState().URL)
}

// readDashState reads the managed web dash's state file
// ($SEXTANT_HOME/dash.json). It fails loud when the file is absent — the web
// dash is not running — with the command to start it.
func readDashState() dashserve.DashState {
	stateFile := filepath.Join(clictx.Root(), "dash.json")
	state, err := dashserve.ReadStateFile(stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fatal("the web dash is not running — start it with `sextant components start dash`")
		}
		fatal("read state file %s: %v", stateFile, err)
	}
	return state
}

// openInBrowser opens url with the platform's default opener (`open` on darwin,
// `xdg-open` on linux). It returns an error when no opener is available so the
// caller can fall back to printing the URL; on supported platforms it does not
// wait for the browser to exit.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
	return cmd.Start()
}
