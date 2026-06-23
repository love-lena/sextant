//go:build race

package main

// raceDetectorEnabled reports whether the test binary was built with -race.
// It lets a test that is flaky ONLY under the race detector's scheduling skip
// itself under -race while still running (and asserting) normally otherwise.
// Paired with raceflag_norace_test.go. Temporary — remove with TASK-170.
const raceDetectorEnabled = true
