package violet

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// dmConsumer is the ANSWER role's goroutine — the priority path (fix #3). It
// drains operator DMs and answers each from the warm context, immediately. It is
// its own goroutine with its own channel, so it NEVER waits behind a gate turn
// or a deep refresh: a burst of bus events cannot delay an answer. This is the
// whole reason the bar (a few seconds even under load) is met where the
// single-loop bash impl failed (44→85s).
func (v *Violet) dmConsumer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-v.dmCh:
			v.answerDM(ctx, m)
		}
	}
}

// answerDM runs the conversational turn from the warm snapshot and publishes the
// captured reply. Output-capture: the model has no publish tool — the reply text
// IS the answer, and the WRAPPER publishes it, so a forgotten publish is
// structurally impossible (the spike's live bug).
func (v *Violet) answerDM(ctx context.Context, m Message) {
	v.mu.Lock()
	v.answering++
	v.mu.Unlock()
	defer func() {
		v.mu.Lock()
		v.answering--
		v.mu.Unlock()
	}()

	dm := frameText(m.Record)
	snapshot, _ := v.warm.get()

	// Per-turn deadline: an answer that can't land promptly fails loud rather
	// than hanging the operator (fail-loud, never a silent hang). The bar is a
	// few seconds; the deadline is generous headroom over a warm turn.
	turnCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reply, err := v.model.turn(turnCtx, v.cfg.ConvModel, turnRequest{
		System:    conversationalSystem,
		MaxTokens: 256, // ≤250-char answers; the bar is terse + plain
		Messages:  []apiMessage{{Role: "user", Content: answerPrompt(snapshot, dm)}},
	})
	if err != nil {
		v.cfg.Logf("violet: answer turn failed: %v", err)
		// Fail-loud to the operator rather than silent: a brief honest note.
		reply = "I hit a snag answering just now — try me again in a moment."
	}

	reply = trimReply(reply)
	rec := chatMessage(reply)
	if _, perr := v.bus.PublishMsg(ctx, v.dmSubject, rec); perr != nil {
		v.cfg.Logf("violet: publish reply failed: %v", perr)
		return
	}
	v.cfg.Logf("violet: answered operator DM (%d chars)", len(reply))

	// An operator DM is always significant — wake the deep pass so the context
	// reflects the just-arrived exchange for the next question. This runs AFTER
	// the answer (the answer used the already-warm context), so it never delays
	// the reply.
	v.requestWake()
}

// gateWorker is the GATE role's goroutine: it drains the bounded candidate queue
// and runs a cheap haiku WAKE/SKIP turn per candidate. Candidates already passed
// the keyword pre-filter (fix #2), so the gate runs rarely. A WAKE signals the
// deep refresher; a SKIP costs only the one classification. The worker is
// single-threaded by design (serial haiku turns) but bounded — and crucially it
// shares NOTHING with the DM path, so even a full gate backlog leaves answers
// untouched.
func (v *Violet) gateWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-v.gateCh:
			v.gate(ctx, m)
		}
	}
}

func (v *Violet) gate(ctx context.Context, m Message) {
	event := describeEvent(m)
	turnCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	verdict, err := v.model.turn(turnCtx, v.cfg.GateModel, turnRequest{
		System:    gateSystem,
		MaxTokens: 8, // one word: WAKE or SKIP
		Messages:  []apiMessage{{Role: "user", Content: gatePrompt(event)}},
	})
	if err != nil {
		// A gate failure leans WAKE: an over-eager wake costs one deep pass; a
		// dropped significant event costs staleness. Bias toward freshness.
		v.cfg.Logf("violet: gate turn failed (waking to be safe): %v", err)
		v.requestWake()
		return
	}
	if strings.Contains(strings.ToUpper(verdict), "WAKE") {
		v.cfg.Logf("violet: gate WAKE on event from %s", short(m.Author))
		v.requestWake()
		return
	}
	// SKIP: gate only, no deep work. Most events land here on a busy bus.
}

// deepRefresher is the HOME-MANAGER role's goroutine. It runs a deep pass when
// the gate wakes it (the primary trigger) or on the slow safety interval (the
// fallback). One pass: gather the live workspace, run the sonnet curation turn,
// capture its snapshot into the warm context, and write the curated home
// projection. It is separate from the answer and gate paths, so a long sonnet
// turn never blocks an answer.
func (v *Violet) deepRefresher(ctx context.Context) {
	ticker := time.NewTicker(v.cfg.SafetyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-v.wakeCh:
			v.deepPass(ctx, "wake")
		case <-ticker.C:
			v.deepPass(ctx, "safety-tick")
		}
	}
}

// deepPass is one curation + context-refresh pass. The wrapper gathers the live
// state (read-only artifact sweep) and writes the curated home (CAS) — the model
// supplies the judgement and the snapshot, output-captured. The model has no
// MCP/artifact tools in this build, so it never tries to write a file it can't
// (Bugs #2).
func (v *Violet) deepPass(ctx context.Context, reason string) {
	passCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	ws, err := gatherWorkspace(passCtx, v.bus)
	if err != nil {
		v.cfg.Logf("violet: deep pass (%s) gather failed: %v", reason, err)
		return
	}

	snapshot, err := v.model.turn(passCtx, v.cfg.DeepModel, turnRequest{
		System:    homeManagerSystem,
		MaxTokens: 1024,
		Messages:  []apiMessage{{Role: "user", Content: refreshPrompt(ws.renderForCuration())}},
	})
	if err != nil {
		v.cfg.Logf("violet: deep pass (%s) curation turn failed: %v", reason, err)
		return
	}

	// Output-capture: the model's reply IS the snapshot. Swap it into the warm
	// context so the next answer is current. set() ignores an empty snapshot, so
	// a failed turn never blanks the context the answers depend on.
	v.warm.set(snapshot)

	// Write the curated home projection. The pinned block is the ranked review
	// queue (the wrapper's deterministic ranking — default blocks-most-downstream;
	// v1 uses queue order); the greeting note is the curated state line derived
	// from the counts. The model's judgement informs the snapshot; the durable
	// curated record is the home artifact the dash reads.
	v.writeHome(passCtx, ws)
	v.cfg.Logf("violet: deep pass (%s) refreshed context + home (%d review, %d goals)",
		reason, len(ws.reviewQueue), len(ws.goals))
}

// writeHome persists the curated `home` projection (read → CAS-update, or create
// first time). signal-not-manage: this is the ONE artifact violet owns and
// writes — never an owner's artifact or review.state.
func (v *Violet) writeHome(ctx context.Context, ws gatheredWorkspace) {
	proj := curateHome(ws)
	rec := proj.marshal()

	if !v.homeKnown {
		if art, err := v.bus.GetArtifact(ctx, "home"); err == nil {
			v.homeRev = art.Revision
			v.homeKnown = true
		}
	}
	if !v.homeKnown {
		rev, err := v.bus.CreateArtifact(ctx, "home", rec)
		if err != nil {
			// A create race (someone created it first) → fall through to update.
			if art, gerr := v.bus.GetArtifact(ctx, "home"); gerr == nil {
				v.homeRev = art.Revision
				v.homeKnown = true
			} else {
				v.cfg.Logf("violet: create home failed: %v", err)
				return
			}
		} else {
			v.homeRev, v.homeKnown = rev, true
			return
		}
	}
	rev, err := v.bus.UpdateArtifact(ctx, "home", rec, v.homeRev)
	if err != nil {
		// Stale CAS: re-read and let the next pass write at the fresh rev.
		if art, gerr := v.bus.GetArtifact(ctx, "home"); gerr == nil {
			v.homeRev = art.Revision
		}
		v.cfg.Logf("violet: update home (stale CAS, will retry next pass): %v", err)
		return
	}
	v.homeRev = rev
}

// curateHome turns the gathered state into the home projection. v1 ranking is
// queue order (default blocks-most-downstream is a follow-up); the greeting note
// is the curated state line: real-call count + quiet count, in her voice. The
// deeper per-item judgement lives in the home-manager's snapshot; this is the
// durable dash record.
func curateHome(ws gatheredWorkspace) homeProjection {
	names := make([]string, 0, len(ws.reviewQueue))
	for _, it := range ws.reviewQueue {
		names = append(names, it.Name)
	}
	note := stateLine(len(ws.reviewQueue), ws.otherCount)
	proj := homeProjection{
		Type:     "document",
		Greeting: homeGreeting{Heading: "Good morning.", Note: note},
	}
	if len(names) > 0 {
		proj.Blocks = append(proj.Blocks, homeBlock{Type: "pinned", Names: names})
	}
	return proj
}

// stateLine is the curated greeting note: plain, calm, headline-first.
func stateLine(realCalls, quiet int) string {
	switch {
	case realCalls == 0:
		return "Nothing needs you right now — it's all in hand."
	case realCalls == 1:
		return "1 real call needs you · the rest is handled."
	default:
		return plural(realCalls, "real call") + " need you · the rest is handled."
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// chatMessage renders a reply as a chat.message record (the shape the dash + MCP
// channel render as text).
func chatMessage(text string) json.RawMessage {
	b, _ := json.Marshal(struct {
		Type string `json:"$type"`
		Text string `json:"text"`
	}{Type: "chat.message", Text: text})
	return b
}

// describeEvent renders a frame for the gate: who, where, and what. The author
// and subject are the bus-stamped truth; the text is the record's content.
func describeEvent(m Message) string {
	who := m.Author
	if who == "" {
		who = "someone"
	}
	return who + " on " + m.Subject + ": " + frameText(m.Record)
}
