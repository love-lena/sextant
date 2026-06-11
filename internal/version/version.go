// Package version carries the build identity every sextant binary reports.
// Release builds stamp Version with the tag via -ldflags (scripts/release.sh);
// anything else reports "dev". The commit rides along for free from the VCS
// info the Go toolchain embeds at build time.
package version

import "runtime/debug"

// Version is the release tag ("v0.1.0"), or "dev" when the build was not
// stamped.
var Version = "dev"

// String is the human form: the version plus the short commit when the build
// carries VCS info — "v0.1.0 (1a2b3c4d5e6f)", or "dev (1a2b3c4d5e6f, dirty)"
// for a from-source build with uncommitted changes.
func String() string {
	rev, dirty := vcs()
	switch {
	case rev == "":
		return Version
	case dirty:
		return Version + " (" + rev + ", dirty)"
	default:
		return Version + " (" + rev + ")"
	}
}

func vcs() (rev string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty
}
