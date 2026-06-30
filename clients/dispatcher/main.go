// Command sextant-dispatch is the reference dispatcher (TASK-25): it stands up
// agents on demand and keeps them revivable.
//
//  1. Connect to the bus as a dispatcher identity (its own creds) and subscribe
//     to a spawn-request subject (default msg.topic.spawn).
//  2. On each spawn.request record, mint a NEW named client identity for the
//     child (kind=agent) and launch the harness that joins it to the bus under
//     that identity. The child is registered as a revivable managed agent — a
//     resumable one-shot (ADR-0045) that does its task and exits, then re-spawns
//     in-process when a later message is addressed to it (revive-on-message).
//  3. Publish a spawn.ack with the new id and the spawn lineage (job + parent).
//
// Recursion falls out for free: a spawned child can itself publish a
// spawn.request, which the dispatcher (still subscribed) honours like any other —
// so a spawn tree can grow itself.
//
// NAMING: a run is identified by a ULID, not an invented persona. An explicit
// nickname on the request (e.g. a workflow run's own name) is honoured; with none,
// the child is minted under the run's own id — the spawn.request frame ULID. There
// is no auto-naming: a name describes the run, never a cute agent identity.
//
// THE RECIPE IS A SWAPPABLE SEAM: --harness is a plain `sh -c CMD` with env vars,
// and the reference recipe (clients/dispatcher/recipes/pi.sh) launches a headless
// `pi` worker — the work engine's sole harness (ADR-0052) — wired to the CHILD's
// own creds. What is mobilized is swapped by pointing --harness at a different
// recipe; WHAT to do is swapped via the prompt ($SX_PROMPT) and the recipe's
// overridable role prompt. A future "run workflow X" recipe slots in the same
// way, no harness rewrite.
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
// coordinated separately.
//
// PoC scope: no job store, no spawn-rate limiting, no persistence of handled
// requests across a restart (handled-request dedup is in-memory, per process);
// child processes are reaped when the dispatcher is signalled.
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
	"sync"
	"syscall"
	"time"

	"github.com/love-lena/sextant/conventions/spawn/go"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

// DefaultModel is the model the dispatcher uses when a spawn.request carries no
// Model field (TASK-245). A step that declares no model explicitly runs on this.
const DefaultModel = "claude-haiku-4-5"

// SupportedModels is the EXPLICIT allowed set of models a spawn.request may
// declare (TASK-245 AC#3). A request declaring a model outside this set FAILS
// LOUD at dispatch — no silent fallback to the default, no worker spawned on it.
// Extend the set by adding to this map; a PR here is the intentional gate.
var SupportedModels = map[string]bool{
	"claude-haiku-4-5":           true,
	"claude-sonnet-4-5":          true,
	"claude-sonnet-4-5-20251001": true,
	"claude-opus-4-5":            true,
	"claude-sonnet-4-6":          true,
	"claude-opus-4-8":            true,
}

// resolveModel resolves the model for a spawn request: if the request declares
// a model it is validated and returned; if empty the DefaultModel is returned;
// if the declared model is not in SupportedModels an error is returned (AC#3:
// fail loud, no silent fallback, no worker spawned on the default). The error
// is the dispatcher's pre-spawn gate — the worker is never launched.
func resolveModel(requested string) (string, error) {
	if requested == "" {
		return DefaultModel, nil
	}
	if !SupportedModels[requested] {
		return "", fmt.Errorf("unsupported model %q: not in the dispatcher's supported-model set (see SupportedModels in clients/dispatcher/main.go); declare a supported model or omit for the default (%s)", requested, DefaultModel)
	}
	return requested, nil
}

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
	workdir := fs.String("workdir", "", "directory for per-child creds files (default: a fresh temp dir)")
	maxSpawns := fs.Int("max", 0, "exit after dispatching this many spawns (0 = run until signalled)")
	deadline := fs.Duration("deadline", 0, "fail loud if no spawn.request arrives within this duration (0 = wait indefinitely)")
	_ = fs.Parse(os.Args[1:])

	if *creds == "" || *harness == "" || (*issuerCreds == "" && !*onBehalf) {
		fatal("usage: sextant-dispatch --creds F --store DIR --harness CMD (--issuer-creds F | --on-behalf) [--subject S] [--max N] [--deadline D]")
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
	defer func() { _ = c.Close() }()

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
		defer func() { _ = iss.Close() }()
		mint = iss.Register
		logf("minting children via the operator/enroll issuer")
	}

	d := &dispatcher{
		ctx: ctx, c: c, mint: mint, store: *store, subject: *subject,
		kind: *kind, harness: *harness, workdir: *workdir,
		seen: map[string]bool{}, agents: map[string]*managedAgent{},
		done: make(chan struct{}, 1), started: make(chan struct{}, 1),
		maxSpawns: *maxSpawns,
	}
	logf("dispatcher up as %s (%s); watching %s for spawn.request", c.DisplayName(), short(c.ID()), *subject)

	// Deliver-NEW, not DeliverAll: a dispatcher acts on spawn.requests published
	// while it is up. Replaying the whole retained spawn log on every (re)start —
	// the old DeliverAll behaviour — re-spawned a fresh worker for every request
	// ever made, since the dedup set is in-memory; the registry filled with dead
	// duplicates. Drain-and-revive (ADR-0045) keeps dormant agents revivable from
	// the registry instead, so a restart needs no replay.
	sub, err := c.Subscribe(ctx, *subject, d.handle)
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
	ctx     context.Context // the signal context; launched children are bound to it
	c       *sextant.Client
	mint    mintFunc
	store   string
	subject string
	kind    string
	harness string
	workdir string

	mu        sync.Mutex
	seen      map[string]bool          // spawn.request frame ids already handled (dedup within a run)
	agents    map[string]*managedAgent // every agent this dispatcher minted, by id — for revive-on-message
	children  []*os.Process            // launched harness processes, for shutdown reaping
	wakeSubs  []sextant.Subscription   // standing per-agent wake subscriptions (inbox + DM topics)
	count     int
	maxSpawns int
	done      chan struct{}
	started   chan struct{} // closed-once signal that the first request was handled
}

// managedAgent is one agent the dispatcher stood up and now keeps revivable. The
// identity (id, nick, creds) is durable; the process is not. running guards against
// double-spawning while a worker is alive — a wake for a running agent is left to
// the live worker (which subscribes its own inbox + DMs); a wake for a dormant one
// re-spawns the harness, resuming its pi session (ADR-0045 drain-and-revive).
type managedAgent struct {
	id         string
	nick       string
	credsPath  string
	job        string
	model      string // resolved model for this agent (TASK-245); set at spawn time
	running    bool
	subscribed bool // wake subjects subscribed once, on first manage()
}

// handle processes one frame on the spawn subject. It acts only on spawn.request
// records (ignoring its own echoed spawn.acks and anything else), dedups by
// request frame id, dispatches, and acks — always, success or failure.
func (d *dispatcher) handle(m sextant.Message) {
	req, ok := spawn.ParseSpawnRequest(m.Frame.Record)
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
	// A run is identified by a ULID, not an invented persona. An explicit nickname on
	// the request (e.g. a workflow run's own name) is honoured; absent, the run's own
	// id (the spawn.request frame ULID) is the name — no auto-naming.
	nick := req.Nickname
	if nick == "" {
		nick = reqID
	}
	model, merr := resolveModel(req.Model)
	logf("spawn.request %s from %s: nick=%q job=%q model=%q", short(reqID), short(parent), nick, req.Job, model)

	ack := spawn.SpawnAck{RequestID: reqID, Job: req.Job, Parent: parent, Nickname: nick}
	var id string
	var err error
	if merr != nil {
		// Model is unsupported — fail loud at dispatch, before any mint or launch
		// (TASK-245 AC#3). The ack carries the error so the coordinator surfaces it.
		err = merr
	} else {
		id, err = d.spawn(req, nick, model)
	}
	if err != nil {
		ack.Status = spawn.StatusError
		ack.Error = err.Error()
		logf("spawn for %q failed: %v", nick, err)
	} else {
		ack.Status = spawn.StatusOK
		ack.ID = id
		logf("spawned %s as %s (%s)", nick, short(id), d.kind)
	}
	if err := d.c.Publish(context.Background(), d.subject, ack.Marshal()); err != nil {
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
// joins the child to the bus under that identity, registers it as a revivable
// managed agent (a standing wake subscription on its inbox + DM topics), and
// returns the minted id. The worker is a resumable one-shot (ADR-0045): it does its
// task, reports, and exits; a later message addressed to it re-spawns it (resuming
// its session) via the wake subscription.
func (d *dispatcher) spawn(req spawn.SpawnRequest, nick, model string) (string, error) {
	issued, err := d.mint(context.Background(), nick, d.kind)
	if err != nil {
		return "", fmt.Errorf("mint child: %w", err)
	}
	credsPath := filepath.Join(d.workdir, issued.ID+".creds")
	if err := os.WriteFile(credsPath, []byte(issued.Creds), 0o600); err != nil {
		return issued.ID, fmt.Errorf("write child creds: %w", err)
	}

	ag := &managedAgent{id: issued.ID, nick: nick, credsPath: credsPath, job: req.Job, model: model}
	d.mu.Lock()
	d.agents[ag.id] = ag
	ag.running = true // claim before launch so a racing wake can't double-spawn
	d.mu.Unlock()

	if err := d.launchHarness(ag, req.Prompt); err != nil {
		d.mu.Lock()
		ag.running = false
		d.mu.Unlock()
		return issued.ID, fmt.Errorf("launch harness: %w", err)
	}
	d.manage(ag) // subscribe its wake subjects once, for revive-on-message
	return issued.ID, nil
}

// manage subscribes a managed agent's wake subjects ONCE: its inbox
// (msg.client.<id>) and the two sorted-DM topic shapes (this id low or high). A
// message there from anyone but the agent itself wakes a revive — UNLESS the agent
// is currently running, in which case the live worker handles its own traffic.
// Deliver-new (no replay): only messages after the subscription wake it.
func (d *dispatcher) manage(ag *managedAgent) {
	d.mu.Lock()
	if ag.subscribed {
		d.mu.Unlock()
		return
	}
	ag.subscribed = true
	d.mu.Unlock()

	subjects := []string{
		sx.ClientSubject(ag.id),        // msg.client.<id> — direct inbox ping
		"msg.topic.dm." + ag.id + ".*", // DM where this agent sorts low
		"msg.topic.dm.*." + ag.id,      // DM where this agent sorts high
	}
	for _, subj := range subjects {
		sub, err := d.c.Subscribe(d.ctx, subj, func(m sextant.Message) { d.onAgentWake(ag, m) })
		if err != nil {
			logf("wake-subscribe %s for %s: %v (revive on that subject disabled)", subj, short(ag.id), err)
			continue
		}
		d.mu.Lock()
		d.wakeSubs = append(d.wakeSubs, sub)
		d.mu.Unlock()
	}
	logf("managing %s (%s): revivable on inbox + DM", ag.nick, short(ag.id))
}

// onAgentWake revives a dormant managed agent when a message is addressed to it. It
// ignores the agent's own echo and the dispatcher's, and skips a wake for an agent
// that is already running (the live worker is subscribed to the same subjects and
// handles it). The claim of running is atomic so two concurrent wakes spawn once.
func (d *dispatcher) onAgentWake(ag *managedAgent, m sextant.Message) {
	from := m.Frame.Author
	if from == ag.id || from == d.c.ID() {
		return
	}
	d.mu.Lock()
	if ag.running {
		d.mu.Unlock()
		return
	}
	ag.running = true
	d.mu.Unlock()

	logf("wake for dormant %s (%s) from %s on %s — reviving", ag.nick, short(ag.id), short(from), m.Subject)
	if err := d.launchHarness(ag, wakePrompt(from, m.Subject, m.Frame.Record)); err != nil {
		d.mu.Lock()
		ag.running = false
		d.mu.Unlock()
		logf("revive %s failed: %v", short(ag.id), err)
	}
}

// launchHarness runs the configured harness for one agent with the given prompt,
// under the child's own creds, and marks the agent dormant when the process exits.
// The caller must have claimed ag.running first (so a racing wake spawns once).
func (d *dispatcher) launchHarness(ag *managedAgent, prompt string) error {
	// SX_AGENT_MODEL relays the per-step model declared in the spawn.request to the
	// pi recipe (TASK-245). The recipe already reads SX_AGENT_MODEL and passes it to
	// pi --model; ag.model was resolved and validated at handle() time, so this is the
	// confirmed declared model (or the default if none was declared).
	model := ag.model
	if model == "" {
		model = DefaultModel
	}
	env := []string{
		"SEXTANT_CREDS=" + ag.credsPath,
		"SEXTANT_STORE=" + d.store,
		"SX_PROMPT=" + prompt,
		"SX_CHILD_ID=" + ag.id,
		"SX_CHILD_NICK=" + ag.nick,
		"SX_JOB=" + ag.job,
		"SX_AGENT_MODEL=" + model,
	}
	return d.launch("harness["+ag.nick+"]", d.harness, env, func() {
		d.mu.Lock()
		ag.running = false
		d.mu.Unlock()
		logf("agent %s (%s) exited; dormant — revives on next message", ag.nick, short(ag.id))
	})
}

// launch starts command via `sh -c` with the dispatcher's environment plus
// extraEnv, streaming its output to our stderr. It does not block; onExit (if set)
// runs when the process exits. The child is bound to the dispatcher's signal
// context, so a shutdown reaps it.
func (d *dispatcher) launch(name, command string, extraEnv []string, onExit func()) error {
	// The signal context (set in main) governs the lifetime: on shutdown these
	// processes are killed, so a stopped dispatcher leaves no orphans.
	cmd := exec.CommandContext(d.ctx, "sh", "-c", command)
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
		if onExit != nil {
			onExit()
		}
	}()
	return nil
}

// wakePrompt builds the brief for a revived worker from the message that woke it:
// the sender, the subject, and the message text, plus the directive to reply to the
// sender over the bus. The worker resumes its own pi session, so this is the new
// turn's input, not its whole context.
func wakePrompt(from, subject string, record json.RawMessage) string {
	return fmt.Sprintf(
		"A bus message just arrived from %s on %s:\n\n%s\n\nHandle it, then reply to %s over the bus with sextant_reply.",
		from, subject, messageText(record), from,
	)
}

// messageText extracts a human-legible string from an opaque message record: a
// `text` field (the chat.message convention) if present, else the raw JSON.
func messageText(record json.RawMessage) string {
	var probe struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(record, &probe); err == nil && probe.Text != "" {
		return probe.Text
	}
	return string(record)
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
