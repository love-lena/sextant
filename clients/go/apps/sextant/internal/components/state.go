package components

import (
	"bufio"
	"strings"
)

// state.go parses a component's launchd job state from `launchctl print`. A
// runtime is "up" when its job has a live process (state=running or a present
// pid) — registered + runs>0 — not when a TCP port is open (unlike the bus). The
// parse mirrors cmd/sextant/doctor.go's parseLaunchdState: the job's own
// top-level state is the FIRST `state =` (sub-dicts nest their own).

// RunState is what a `launchctl print` query reports for a component.
type RunState struct {
	Loaded  bool   // the job is bootstrapped into the domain
	Running bool   // it has a live process (state=running / a present pid)
	Raw     string // the parsed top-level state string, for display
}

// firstLine returns the first line of s — enough of a launchctl error to show
// without dumping a multi-line dict.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// notLoaded reports whether a launchctl failure means the job is simply not
// loaded (vs a real query error). launchctl uses both a message and exit 113.
func notLoaded(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "could not find") || strings.Contains(o, "no such process") || strings.Contains(o, "not find service")
}

// alreadyLoaded reports whether a bootstrap failure is the benign
// already-bootstrapped case, so a restart's re-bootstrap is idempotent.
func alreadyLoaded(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "already") || strings.Contains(o, "bootstrap failed: 5")
}

// parsePrint pulls loaded/running from `launchctl print` output. A printed job
// is loaded; running is read from `state = running` or a present `pid = N`.
func parsePrint(out string) RunState {
	st := RunState{Loaded: true}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if st.Raw == "" && strings.HasPrefix(line, "state = ") {
			st.Raw = strings.TrimSpace(strings.TrimPrefix(line, "state = "))
			if st.Raw == "running" {
				st.Running = true
			}
			continue
		}
		if !st.Running && strings.HasPrefix(line, "pid = ") {
			st.Running = true
		}
	}
	return st
}
