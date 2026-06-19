// Command spawn-poc is the M5.1 spawn-spike wake-loop supervisor (TASK-70).
//
// A `claude -p` / `codex exec` agent is one-shot: it runs, publishes, and goes
// offline — it is never retriggered. This supervisor is what makes such an agent
// wake-on-message WITHOUT any core protocol change. It is its OWN bus client
// (pkg/sextant) — the simplest shape of the M5.2 dispatcher, built so M5.2 grows
// it rather than replaces it:
//
//  1. Connect to the bus as a dispatcher identity (its own creds).
//  2. Subscribe to the spawned agent's DM subject, msg.client.<agent-id>
//     (and any extra subjects the agent asked to be watched on — see --watch).
//  3. On each inbound message (from anyone but the agent itself), re-invoke the
//     agent via --on-wake, feeding the message text as the prompt. The agent
//     wakes under its resume-stable identity, reads the instruction, and acts.
//
// The companion to this loop is an agent-side EXIT hook (lena, 2026-06-12): a
// one-shot agent that wants to keep listening has no way to say so before it
// ends, so a Stop/exit hook asks it "do you need to subscribe to anything before
// ending?" and the agent's answer becomes additional --watch subjects here. The
// hook is an M5.2 client concern; this supervisor is the half that consumes its
// output (the watch set). See docs/demos/spawn-spike-notes.md.
//
// PoC scope: no dedup/coalescing of overlapping wakes, no persistence of the
// watch set across restarts; --once exits after the first wake. It runs only on
// a throwaway bus during the spike (never the operator's live bus).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
)

func main() {
	fs := flag.NewFlagSet("spawn-poc", flag.ExitOnError)
	creds := fs.String("creds", os.Getenv("SEXTANT_CREDS"), "dispatcher credentials file (issue with `sextant clients register`)")
	store := fs.String("store", os.Getenv("SEXTANT_STORE"), "bus store dir for bus.json discovery")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	agent := fs.String("agent", "", "spawned agent's bus client id; its DM subject (msg.client.<id>) is always watched")
	watch := multiFlag{}
	fs.Var(&watch, "watch", "additional subject to watch and wake the agent on (repeatable; e.g. msg.topic.demo)")
	onWake := fs.String("on-wake", "", "command run (via `sh -c`) on each inbound message; the message text is exported as $SX_WAKE_TEXT, its author id as $SX_WAKE_FROM")
	once := fs.Bool("once", false, "exit after the first wake (PoC demo mode)")
	deadline := fs.Duration("deadline", 0, "fail loud if no message arrives within this duration (0 = wait indefinitely)")
	wakeTimeout := fs.Duration("wake-timeout", 3*time.Minute, "max time for one --on-wake invocation before it is killed")
	_ = fs.Parse(os.Args[1:])

	if *creds == "" || *agent == "" || *onWake == "" {
		fatal("usage: spawn-poc --creds F --store DIR --agent <client-id> --on-wake CMD [--watch SUBJ ...] [--once] [--deadline D]")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := sextant.Connect(connCtx, sextant.Options{
		CredsPath:    *creds,
		URL:          *url,
		ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		fatal("connect: %v", err)
	}
	defer c.Close()
	logf("supervisor up as %s (%s); watching agent %s", c.DisplayName(), short(c.ID()), short(*agent))

	// The agent's own DM subject is always watched; --watch adds the subjects the
	// agent (via its exit hook) asked to keep listening on.
	subjects := append([]string{"msg.client." + *agent}, watch...)

	woke := make(chan struct{}, 1)
	handle := func(m sextant.Message) {
		from := m.Frame.Author
		if from == *agent || from == c.ID() {
			return // never wake the agent on its own traffic, or on ours
		}
		text := messageText(m.Frame.Record)
		logf("inbound on %s from %s: %q — waking agent", m.Subject, short(from), trunc(text, 80))
		runWake(ctx, *onWake, text, from, *wakeTimeout)
		select {
		case woke <- struct{}{}:
		default:
		}
	}

	for _, subj := range subjects {
		sub, err := c.Subscribe(ctx, subj, handle, sextant.DeliverAll())
		if err != nil {
			fatal("subscribe %s: %v", subj, err)
		}
		defer sub.Stop()
		logf("watching %s", subj)
	}

	var dl <-chan time.Time
	if *deadline > 0 {
		t := time.NewTimer(*deadline)
		defer t.Stop()
		dl = t.C
	}

	for {
		select {
		case <-ctx.Done():
			logf("signalled; shutting down")
			return
		case <-c.Drained():
			logf("bus drained; shutting down")
			return
		case <-dl:
			fatal("deadline %s elapsed with no inbound message (fail-loud)", *deadline)
		case <-woke:
			if *once {
				logf("woke once; done")
				return
			}
		}
	}
}

// runWake invokes the on-wake command with the inbound message in the
// environment. It streams the child's output to our stderr (so the demo log
// captures the woken agent's run) and bounds it with wakeTimeout — a wedged
// re-invocation never hangs the supervisor (fail-loud, fail-early).
func runWake(parent context.Context, cmd, text, from string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Env = append(os.Environ(), "SX_WAKE_TEXT="+text, "SX_WAKE_FROM="+from)
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	c.Stdin = nil
	start := time.Now()
	if err := c.Run(); err != nil {
		logf("on-wake exited with error after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return
	}
	logf("on-wake completed in %s", time.Since(start).Round(time.Millisecond))
}

// messageText pulls the human text out of a chat.message record; for any other
// lexicon it returns the raw JSON, so the woken agent still gets the payload.
func messageText(record json.RawMessage) string {
	var msg struct {
		Type string `json:"$type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(record, &msg); err == nil && msg.Text != "" {
		return msg.Text
	}
	return string(record)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "spawn-poc: "+format+"\n", a...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "spawn-poc: "+format+"\n", a...)
	os.Exit(1)
}
