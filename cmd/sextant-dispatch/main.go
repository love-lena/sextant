// Command sextant-dispatch is the M5.2 reference dispatcher (TASK-25). It
// graduates the M5.1 spawn spike (cmd/spawn-poc) from "supervise one known agent"
// into "stand up agents on demand":
//
//  1. Connect to the bus as a dispatcher identity (its own creds) and subscribe
//     to a spawn-request subject (default msg.topic.spawn).
//  2. On each spawn.request record, mint a NEW named client identity for the
//     child (kind=agent), launch the harness that joins it to the bus under that
//     identity, and — if --on-wake is set — launch a per-child supervisor
//     (cmd/spawn-poc, its own bus client) that watches the child's DM and
//     re-invokes the one-shot harness on inbound: the wake loop.
//  3. Publish a spawn.ack with the new id and the spawn lineage (job + parent).
//
// Recursion falls out for free: a spawned child can itself publish a
// spawn.request, which the dispatcher (still subscribed) honours like any other —
// so a spawn tree can grow itself.
//
// NAMING: a spawn.request with no nickname is auto-named before minting — the
// dispatcher asks a cheap Haiku model (--api-key) for a unique, evocative handle,
// verified against the live clients directory, and mints the child under it. It is
// best-effort and NEVER blocks the spawn: no key, a model error, or repeated
// collisions all fall back to the safe agent-<id> default (see naming.go).
//
// THE RECIPE IS A SWAPPABLE SEAM: --harness is a plain `sh -c CMD` with env vars,
// and the default reference recipe (cmd/sextant-dispatch/recipes/agent.sh) launches
// a capable, self-directing `claude` agent wired to the CHILD's own creds. What is
// mobilized is swapped by pointing --harness at a different recipe; WHAT to do is
// swapped via the prompt ($SX_PROMPT) and the recipe's overridable role prompt. A
// future "run workflow X" recipe slots in the same way, no harness rewrite.
//
// SECURITY: a spawn.request carries DATA only (a prompt and lineage labels),
// never a command. The dispatcher always runs its OWN configured --harness, so a
// request from any bus client can never inject code onto the dispatcher's host.
// The lineage parent is the bus-stamped frame author, never a self-declared field.
//
// MINTING: in this reference build the dispatcher mints child identities through
// the existing issuance path (--issuer-creds, an operator/enroll credential read
// locally — locality is the trust, ADR-0020). AC#6 (mint-on-behalf) replaces that
// with a SCOPED bus op so the dispatcher mints with its OWN agent authority and
// needs no operator credential; it is a serial locked-core change (ADR-0022),
// coordinated separately. See docs/demos/m5-dispatcher-notes.md.
//
// PoC scope: no job store, no spawn-rate limiting, no persistence of handled
// requests across a restart (handled-request dedup is in-memory, per process);
// child processes are reaped when the dispatcher is signalled.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
)

func main() {
	fs := flag.NewFlagSet("sextant-dispatch", flag.ExitOnError)
	creds := fs.String("creds", os.Getenv("SEXTANT_CREDS"), "dispatcher credentials file (its own bus identity; issue with `sextant clients register`)")
	store := fs.String("store", os.Getenv("SEXTANT_STORE"), "bus store dir for bus.json discovery")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	subject := fs.String("subject", "msg.topic.spawn", "subject to watch for spawn.request records and publish spawn.ack to")
	issuerCreds := fs.String("issuer-creds", "", "operator/enroll creds used to mint child identities via the existing issuance path (the parallel demo path; ignored when --on-behalf is set)")
	onBehalf := fs.Bool("on-behalf", false, "mint children with the dispatcher's OWN authority (mint-on-behalf, ADR-0033) instead of an operator/enroll issuer — requires the dispatcher to be a registered kind=dispatcher client")
	kind := fs.String("kind", "agent", "kind to mint spawned clients as")
	harness := fs.String("harness", "", "command (run via `sh -c`) that launches one spawned client; its environment carries SEXTANT_CREDS (the child's), SEXTANT_STORE, $SX_PROMPT, $SX_CHILD_ID, $SX_CHILD_NICK, $SX_JOB")
	onWake := fs.String("on-wake", "", "command the per-child supervisor runs on an inbound DM (passed to spawn-poc --on-wake); empty disables supervision")
	supervisor := fs.String("supervisor", "spawn-poc", "path to the spawn-poc supervisor binary (used when --on-wake is set)")
	wakeTimeout := fs.Duration("wake-timeout", 3*time.Minute, "per-child supervisor --wake-timeout")
	workdir := fs.String("workdir", "", "directory for per-child creds files (default: a fresh temp dir)")
	maxSpawns := fs.Int("max", 0, "exit after dispatching this many spawns (0 = run until signalled)")
	deadline := fs.Duration("deadline", 0, "fail loud if no spawn.request arrives within this duration (0 = wait indefinitely)")
	apiKey := fs.String("api-key", os.Getenv("ANTHROPIC_API_KEY"), "Anthropic API key for Haiku auto-naming of un-nicknamed children (or $ANTHROPIC_API_KEY); empty disables naming (children fall back to agent-<id>)")
	apiBaseURL := fs.String("api-base-url", "", "Anthropic API base URL for the naming call (default: the public API; a mock URL for tests)")
	_ = fs.Parse(os.Args[1:])

	if *creds == "" || *harness == "" || (*issuerCreds == "" && !*onBehalf) {
		fatal("usage: sextant-dispatch --creds F --store DIR --harness CMD (--issuer-creds F | --on-behalf) [--subject S] [--on-wake CMD] [--max N] [--deadline D]")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *workdir == "" {
		d, err := os.MkdirTemp("", "sextant-dispatch-")
		if err != nil {
			fatal("workdir: %v", err)
		}
		*workdir = d
	}

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

	// Minting authority: mint-on-behalf uses the dispatcher's OWN client connection
	// (ADR-0033, requires kind=dispatcher); otherwise it uses an operator/enroll
	// issuer (the existing issuance path). Either returns the same IssuedClient.
	var mint mintFunc
	if *onBehalf {
		mint = c.Register
		logf("minting children via mint-on-behalf (own authority; ADR-0033)")
	} else {
		iss, err := sextant.ConnectIssuer(connCtx, sextant.Options{
			CredsPath:    *issuerCreds,
			URL:          *url,
			ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
			Logf:         func(string, ...any) {},
		})
		if err != nil {
			fatal("issuer connect: %v", err)
		}
		defer iss.Close()
		mint = iss.Register
		logf("minting children via the operator/enroll issuer")
	}

	// The namer picks a unique, evocative name for an un-nicknamed child before
	// minting (Haiku auto-naming). It composes a model picker — nil when no API
	// key is set, so naming degrades to the safe default — with the dispatcher's
	// own ListClients for the uniqueness check. Naming NEVER blocks a spawn.
	nm := namer{
		pick: newHaikuPicker(*apiKey, *apiBaseURL, nil),
		list: func(ctx context.Context) ([]string, error) {
			infos, err := c.ListClients(ctx)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(infos))
			for _, i := range infos {
				if i.DisplayName != "" {
					names = append(names, i.DisplayName)
				}
			}
			return names, nil
		},
	}
	if *apiKey == "" {
		logf("Haiku auto-naming disabled (no --api-key / $ANTHROPIC_API_KEY); un-nicknamed children use the agent-<id> fallback")
	}

	d := &dispatcher{
		ctx: ctx, c: c, mint: mint, store: *store, creds: *creds, subject: *subject,
		kind: *kind, harness: *harness, onWake: *onWake, supervisor: *supervisor,
		wakeTimeout: *wakeTimeout, workdir: *workdir, namer: nm,
		seen: map[string]bool{}, done: make(chan struct{}, 1), started: make(chan struct{}, 1),
		maxSpawns: *maxSpawns,
	}
	logf("dispatcher up as %s (%s); watching %s for spawn.request", c.DisplayName(), short(c.ID()), *subject)

	sub, err := c.Subscribe(ctx, *subject, d.handle, sextant.DeliverAll())
	if err != nil {
		fatal("subscribe %s: %v", *subject, err)
	}
	defer sub.Stop()

	// The deadline is a STARTUP liveness guard — fail loud if no spawn.request ever
	// arrives (misconfigured subject, unreachable bus) — not an overall run limit.
	// It is disabled the moment the first request is handled.
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
			fatal("deadline %s elapsed with no spawn.request (fail-loud)", *deadline)
		case <-d.started:
			dl = nil // saw the first request; the startup deadline no longer applies
		case <-d.done:
			logf("dispatched %d spawn(s); done", *maxSpawns)
			stop() // cancel the context so launched children are reaped
			return
		}
	}
}

// mintFunc mints one child identity; satisfied by both Issuer.Register (the
// operator/enroll issuance path) and Client.Register (mint-on-behalf, ADR-0033).
type mintFunc func(ctx context.Context, displayName, kind string) (sextant.IssuedClient, error)

type dispatcher struct {
	ctx         context.Context // the signal context; launched children are bound to it
	c           *sextant.Client
	mint        mintFunc
	store       string
	creds       string // the dispatcher's own creds (handed to per-child supervisors)
	subject     string
	kind        string
	harness     string
	onWake      string
	supervisor  string
	wakeTimeout time.Duration
	workdir     string
	namer       namer // picks a unique name for an un-nicknamed child (Haiku auto-naming)

	mu        sync.Mutex
	seen      map[string]bool // spawn.request frame ids already handled (dedup across DeliverAll replay)
	children  []*os.Process   // launched harness + supervisor processes, for shutdown reaping
	count     int
	maxSpawns int
	done      chan struct{}
	started   chan struct{} // closed-once signal that the first request was handled
}

// handle processes one frame on the spawn subject. It acts only on spawn.request
// records (ignoring its own echoed spawn.acks and anything else), dedups by
// request frame id, dispatches, and acks — always, success or failure.
func (d *dispatcher) handle(m sextant.Message) {
	req, ok := parseSpawnRequest(m.Frame.Record)
	if !ok {
		return
	}
	reqID := m.Frame.ID
	d.mu.Lock()
	if d.seen[reqID] {
		d.mu.Unlock()
		return
	}
	d.seen[reqID] = true
	d.mu.Unlock()
	select { // first valid request disables the startup deadline
	case d.started <- struct{}{}:
	default:
	}

	parent := m.Frame.Author // the trusted lineage parent
	nick := req.Nickname
	if nick == "" {
		// No nickname on the request: pick a unique, evocative one via Haiku
		// before minting (auto-naming). Bounded + best-effort — pickName falls
		// back to agent-<id> on any failure, so a spawn is never blocked on it.
		fallback := "agent-" + short(reqID)
		nick = d.namer.pickName(d.ctx, req.Prompt, req.Job, fallback)
	}
	logf("spawn.request %s from %s: nick=%q job=%q", short(reqID), short(parent), nick, req.Job)

	ack := SpawnAck{RequestID: reqID, Job: req.Job, Parent: parent, Nickname: nick}
	id, err := d.spawn(req, nick)
	if err != nil {
		ack.Status = statusError
		ack.Error = err.Error()
		logf("spawn for %q failed: %v", nick, err)
	} else {
		ack.Status = statusOK
		ack.ID = id
		logf("spawned %s as %s (%s)", nick, short(id), d.kind)
	}
	if err := d.c.Publish(context.Background(), d.subject, ack.marshal()); err != nil {
		logf("publish spawn.ack: %v", err)
	}

	d.mu.Lock()
	d.count++
	reached := d.maxSpawns > 0 && d.count >= d.maxSpawns
	d.mu.Unlock()
	if reached {
		select {
		case d.done <- struct{}{}:
		default:
		}
	}
}

// spawn mints a named child identity, writes its creds, launches the harness that
// joins the child to the bus under that identity, and (when --on-wake is set)
// launches a per-child supervisor for the wake loop. It returns the minted id.
func (d *dispatcher) spawn(req SpawnRequest, nick string) (string, error) {
	issued, err := d.mint(context.Background(), nick, d.kind)
	if err != nil {
		return "", fmt.Errorf("mint child: %w", err)
	}
	credsPath := filepath.Join(d.workdir, issued.ID+".creds")
	if err := os.WriteFile(credsPath, []byte(issued.Creds), 0o600); err != nil {
		return issued.ID, fmt.Errorf("write child creds: %w", err)
	}

	childEnv := []string{
		"SEXTANT_CREDS=" + credsPath,
		"SEXTANT_STORE=" + d.store,
		"SX_PROMPT=" + req.Prompt,
		"SX_CHILD_ID=" + issued.ID,
		"SX_CHILD_NICK=" + nick,
		"SX_JOB=" + req.Job,
	}
	if err := d.launch("harness["+nick+"]", d.harness, childEnv); err != nil {
		return issued.ID, fmt.Errorf("launch harness: %w", err)
	}

	if d.onWake != "" {
		// The supervisor is its OWN bus client (cmd/spawn-poc): it connects as the
		// dispatcher (--creds), watches the child's DM, and on an inbound message
		// re-invokes --on-wake. SEXTANT_CREDS in its environment is the CHILD's, so
		// the woken harness rejoins under the child's identity (spawn-poc's explicit
		// --creds flag still wins for the supervisor's own connection).
		args := []string{
			"--creds", d.creds, "--store", d.store, "--agent", issued.ID,
			"--on-wake", d.onWake, "--wake-timeout", d.wakeTimeout.String(),
		}
		if err := d.launchCmd("supervisor["+nick+"]", d.supervisor, args, childEnv); err != nil {
			return issued.ID, fmt.Errorf("launch supervisor: %w", err)
		}
	}
	return issued.ID, nil
}

// launch starts command via `sh -c` with the dispatcher's environment plus
// extraEnv, streaming its output to our stderr; it does not wait (the harness is
// one-shot and the supervisor is long-lived). The child is bound to the
// dispatcher's signal context, so a shutdown reaps it.
func (d *dispatcher) launch(name, command string, extraEnv []string) error {
	return d.launchCmd(name, "sh", []string{"-c", command}, extraEnv)
}

func (d *dispatcher) launchCmd(name, bin string, args, extraEnv []string) error {
	// The signal context (set in main) governs the lifetime: on shutdown these
	// processes are killed, so a stopped dispatcher leaves no orphans.
	cmd := exec.CommandContext(d.ctx, bin, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	d.mu.Lock()
	d.children = append(d.children, cmd.Process)
	d.mu.Unlock()
	go func() {
		err := cmd.Wait()
		logf("%s (pid %d) exited: %v", name, pid, err)
	}()
	return nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "sextant-dispatch: "+format+"\n", a...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "sextant-dispatch: "+format+"\n", a...)
	os.Exit(1)
}
