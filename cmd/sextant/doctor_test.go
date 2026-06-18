package main

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/buscfg"
	"github.com/love-lena/sextant/pkg/conninfo"
)

func TestRunDoctorMissingDiscovery(t *testing.T) {
	store := t.TempDir() // no bus.json
	var out strings.Builder
	runDoctor(&out, store)
	s := out.String()
	if !strings.Contains(s, "MISSING") {
		t.Errorf("doctor on a store with no bus.json should report MISSING discovery; got:\n%s", s)
	}
	// No recorded address ⇒ no reachability line (nothing to dial).
	if strings.Contains(s, "reachable:") {
		t.Errorf("doctor should not print a reachability line without a recorded URL; got:\n%s", s)
	}
}

func TestRunDoctorReachableAndPortHint(t *testing.T) {
	// A live listener stands in for the bus; bus.json points at it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	store := t.TempDir()
	url := "nats://" + ln.Addr().String()
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: url}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	var out strings.Builder
	runDoctor(&out, store)
	s := out.String()
	if !strings.Contains(s, "reachable: YES") {
		t.Errorf("doctor should report reachable YES for a live listener; got:\n%s", s)
	}
	// Unpinned port ⇒ the pin hint must show (the outage remedy).
	if !strings.Contains(s, "config set port") {
		t.Errorf("doctor should hint at pinning a port when unpinned; got:\n%s", s)
	}
}

func TestRunDoctorUnreachable(t *testing.T) {
	// Reserve then release a port so nothing is listening on the recorded address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	store := t.TempDir()
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: "nats://" + addr}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}
	if err := buscfg.Save(buscfg.Path(store), buscfg.Config{Port: 63527}); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	runDoctor(&out, store)
	s := out.String()
	if !strings.Contains(s, "reachable: NO") {
		t.Errorf("doctor should report reachable NO when nothing listens; got:\n%s", s)
	}
	// A pinned port should report as pinned (no hint).
	if !strings.Contains(s, "pinned") {
		t.Errorf("doctor should report a pinned port; got:\n%s", s)
	}
}

func TestParseLaunchdState(t *testing.T) {
	// Trimmed real `launchctl print` output: the job's own top-level `state`
	// comes first; nested endpoint states (also "state = active") must not win.
	out := `gui/501/homebrew.mxcl.sextant = {
	active count = 1
	state = running
	stdout path = /opt/homebrew/var/log/sextant.log
	endpoints = {
		"x" = {
			state = active
		}
	}
}`
	state, logPath := parseLaunchdState(out)
	if state != "running" {
		t.Errorf("state = %q, want running", state)
	}
	if logPath != "/opt/homebrew/var/log/sextant.log" {
		t.Errorf("logPath = %q, want the sextant log", logPath)
	}

	// A not-running job (the throttle trap) must be detected as non-"running".
	notRunning := fmt.Sprintf("label = x\n\tstate = %s\n", "waiting")
	if st, _ := parseLaunchdState(notRunning); st != "waiting" {
		t.Errorf("state = %q, want waiting", st)
	}
}
