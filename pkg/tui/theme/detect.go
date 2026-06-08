package theme

import (
	"os"

	"github.com/muesli/termenv"
)

// detectLightBackground reports whether the controlling terminal has a light
// background. It queries the terminal via termenv and returns false (assume
// dark) whenever detection is unavailable — a non-terminal stdout, or a terminal
// that does not answer the background query — so Auto has a deterministic dark
// fallback.
func detectLightBackground() bool {
	return !termenv.NewOutput(os.Stdout).HasDarkBackground()
}
