// restart.go owns `doRestart` — the testable body of `sextant daemon
// restart`. The cobra wiring lives in daemon.go.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// doRestart is the testable body of `sextant daemon restart`. We pass
// the cfg loaded once at the top so a config-dir flag change in the
// middle can't confuse the two phases.
func doRestart(w io.Writer, cfg sextantd.Config, stopTimeout, startTimeout time.Duration) error {
	if err := doStop(w, cfg, stopTimeout); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stop phase: %w", err)
		}
	}
	fmt.Fprintf(w, "starting…\n")
	return doStart(w, cfg, startTimeout)
}
