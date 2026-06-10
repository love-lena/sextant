package theme

import (
	"os"

	"github.com/muesli/termenv"
)

// detectLightBackground reports whether the controlling terminal has a light
// background. It queries the terminal via termenv and returns false (assume
// dark) whenever detection is unavailable — a non-terminal stdout (no query is
// issued at all, keeping tests and pipes deterministic), or a terminal that
// does not answer the background query — so Auto has a deterministic dark
// fallback. The query itself is deadline-bounded by termenv (OSCTimeout), so
// detection answers or falls back; it never hangs.
func detectLightBackground() bool {
	return !termenv.NewOutput(os.Stdout).HasDarkBackground()
}
