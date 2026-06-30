package main

// Agent mode (TASK-242, ADR-0048-additive) — the opt-in, long-lived coordinator-AGENT
// review loop layered onto the programmatic shell. It is STRICTLY additive: a run without
// Run.AgentMode is the existing programmatic path, byte-unchanged (the shell never stands
// up or consults an agent — agentEnabled() is the single gate, and every agent-mode call
// site is behind it).
//
// What agent mode adds, and the bright lines it holds:
//
//   - One LONG-LIVED coordinator agent per run, stood up ONCE on adopt via the dispatcher
//     (ADR-0045 drain-and-revive; kept resident across the whole run for context
//     continuity). Its ULID + function is its identity — never a persona (AC#6).
//   - At each completed work/brief step — AFTER the deterministic gates pass — the shell
//     publishes a run.review to the agent and awaits a run.decision. The decision is one of
//     the four FLAT-STEP-MODEL v1 verbs (advance | redo-with-feedback | edit-then-advance |
//     stop). No graph reshaping (branch/insert/skip) in v1: an unknown verb is rejected and
//     the run does NOT advance on it (AC#2).
//   - The shell stays the SOLE single-writer of the run envelope (AC#3). The agent emits a
//     run.decision (a plain msg.* record) and edits DELIVERABLE artifacts directly when it
//     chooses edit-then-advance (its own act, unbounded — AC#6); it NEVER writes the run
//     envelope. checkpoint() remains the one envelope writer.
//   - Agent decisions sit ON the deterministic proof-gate floor and can NEVER bypass it
//     (AC#7): the existence gate (verifyReportedArtifactsExist) runs in runDispatch/runBrief
//     BEFORE the agent is ever consulted, so an advance/done over an absent or fabricated
//     deliverable fails the gate and blocks the run regardless of what the agent returns.
//
// The agent reviewer is the ONE place a coordinator-side client reads artifact CONTENT
// (judging quality is its job) — but that read happens INSIDE the agent (a convention
// client), not in this deterministic shell, which still only relays metadata + refs.

import (
	"fmt"
	"time"

	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

// agentEnabled reports whether this run is in agent mode. The SINGLE gate every agent-mode
// path is behind: false → the programmatic shell behaves exactly as TASK-236 (no review,
// no agent stood up, no run.review/run.decision traffic).
func (co *coordinator) agentEnabled() bool { return co.run.AgentMode }

// standUpCoordinatorAgent spawns the long-lived coordinator-reviewer agent for this run,
// ONCE, via the dispatcher (the same compose-M5.2 path runDispatch uses for workers). It
// records the agent id so subsequent reviews DM it. A no-op if the run is not in agent mode
// or the agent is already up (idempotent on resume). Fail-loud: a stand-up failure is
// returned so the run blocks rather than silently degrading to the programmatic path.
func (co *coordinator) standUpCoordinatorAgent() error {
	if !co.agentEnabled() || co.reviewerAgent != "" {
		return nil
	}
	req := workflow.SpawnRequest{Prompt: co.reviewerPrompt(), Nickname: "run-coordinator", Job: co.run.ID}
	out, err := co.c.PublishMsg(co.ctx, co.spawnSubject, req.Marshal())
	if err != nil {
		return fmt.Errorf("publish coordinator-agent spawn.request: %w", err)
	}
	ack, ok := co.awaitAck(out.ID, co.stepTimeout)
	if !ok {
		return fmt.Errorf("no spawn.ack for coordinator agent within %s", co.stepTimeout)
	}
	if ack.Status != workflow.StatusOK {
		return fmt.Errorf("coordinator-agent dispatch rejected: %s", ack.Error)
	}
	co.mu.Lock()
	co.reviewerAgent = ack.ID
	co.mu.Unlock()
	co.appendActivity("🧭", fmt.Sprintf("agent mode: coordinator reviewer %s resident for the run", short(ack.ID)))
	return nil
}

// reviewerPrompt is the long-lived reviewer agent's standing brief. It tells the agent its
// function (wrap workers, judge each step's output, decide), the EXACT decision vocabulary,
// the single content-read sanction, and the channels it answers on. Identity is the agent's
// ULID + this function — never a persona (AC#6).
func (co *coordinator) reviewerPrompt() string {
	return fmt.Sprintf(
		"You are the long-lived COORDINATOR REVIEWER for run %s (objective: %q). You are a WRAPPER for the dispatched workers — never the author of a deliverable from scratch; substantive work is the workers' job.\n"+
			"On each %s record I send to your inbox, READ the produced artifacts' content (sextant_artifact_get — this is the one place reading content is sanctioned: judging quality is your job) and reply with a %s on %s, with `verb` set to EXACTLY one of:\n"+
			"  - %q: the output is good; proceed to the next step.\n"+
			"  - %q: the output is fundamentally wrong; re-dispatch the SAME step. Put your guidance in `feedback` (it is threaded into the worker's next prompt). Prefer this when rework is substantial.\n"+
			"  - %q: you applied a fix-up edit to the deliverable yourself (you MAY freely edit any deliverable — this is unbounded); then advance. Record what you changed in `reason`.\n"+
			"  - %q: stop the run now.\n"+
			"There is NO branch/insert/skip in v1 — those verbs are rejected. Always set `step` to the reviewed step id. Your reasoning streams to your agent.activity feed automatically; end each review with a turn so the shell sees you came to rest.",
		co.run.ID, co.run.Objective,
		workflow.TypeRunReview, workflow.TypeRunDecision, workflow.RunDecisionSubject(co.run.ID),
		workflow.DecisionAdvance, workflow.DecisionRedo, workflow.DecisionEdit, workflow.DecisionStop,
	)
}

// reviewStep is the agent-mode decision point. Called from a completed work/brief step
// AFTER the deterministic gates have already passed (existence-checked refs), so the agent
// can NEVER advance the run over an absent/fabricated deliverable — the gate ran first
// (AC#7). It publishes a run.review to the resident agent and awaits its run.decision,
// validates the verb against the FLAT-STEP-MODEL v1 set, records the decision on the run's
// activity trail (observability — AC#6 "no silent edit"), and returns the decision for the
// caller to apply. Bounded wait (fail-loud).
//
// reviewStep itself reads NO artifact content (the shell stays content-opaque): it relays
// the produced REFS to the agent, and the agent does the content read. The decision rides a
// msg.* record — the agent never writes the run envelope (single-writer holds, AC#3).
func (co *coordinator) reviewStep(step *workflow.RunStep) (workflow.RunDecision, error) {
	if co.reviewerAgent == "" {
		return workflow.RunDecision{}, fmt.Errorf("agent mode: no coordinator reviewer for step %q (stand-up failed)", step.ID)
	}
	review := workflow.RunReview{
		Step:      step.ID,
		Objective: co.run.Objective,
		Label:     step.Label,
		Produced:  step.Produced,
	}
	// DM the review to the resident agent's inbox (it lands as a follow-up turn on the
	// long-lived session, preserving context continuity — ADR-0045).
	if err := co.c.Publish(co.ctx, sx.ClientSubject(co.reviewerAgent), review.Marshal()); err != nil {
		return workflow.RunDecision{}, fmt.Errorf("publish run.review for step %q: %w", step.ID, err)
	}
	co.appendActivity("👁", fmt.Sprintf("agent review requested for step %q", step.ID))
	dec, ok := co.awaitDecision(step.ID, co.stepTimeout)
	if !ok {
		return workflow.RunDecision{}, fmt.Errorf("coordinator agent never returned a decision for step %q within %s", step.ID, co.stepTimeout)
	}
	if !workflow.IsDecisionVerb(dec.Verb) {
		// A graph-reshaping or unknown verb is REJECTED — never silently treated as advance
		// (AC#2: the decision set is EXACTLY the four flat verbs).
		return workflow.RunDecision{}, fmt.Errorf("coordinator agent returned unsupported decision %q for step %q (v1 allows only advance|redo-with-feedback|edit-then-advance|stop)", dec.Verb, step.ID)
	}
	co.appendActivity("⚖", decisionText(dec))
	return dec, nil
}

// decisionText renders a decision for the run's activity trail so every agent decision —
// including an edit — is observable and attributable (AC#6 "no silent edit bypassing the
// decision/activity trail").
func decisionText(d workflow.RunDecision) string {
	t := fmt.Sprintf("agent decision on %q: %s", d.Step, d.Verb)
	switch {
	case d.Verb == workflow.DecisionRedo && d.Feedback != "":
		t += " — feedback: " + d.Feedback
	case d.Reason != "":
		t += " — " + d.Reason
	}
	return t
}

// awaitDecision blocks until the resident agent posts a run.decision for stepID on the
// decision subject, or the timeout/cancel/shutdown fires. Mirrors awaitStepDone's bounded
// shape (fail-loud).
func (co *coordinator) awaitDecision(stepID string, timeout time.Duration) (workflow.RunDecision, bool) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		if co.isCancelled() {
			return workflow.RunDecision{}, false
		}
		co.mu.Lock()
		dec, ok := co.decisions[stepID]
		if ok {
			delete(co.decisions, stepID) // matched; the NEXT review of this step (a redo loop) waits for a fresh one
		}
		co.mu.Unlock()
		if ok {
			return dec, true
		}
		select {
		case <-co.decCh:
		case <-co.ctlCh: // a cancel must abort the decision wait promptly
		case <-t.C:
			return workflow.RunDecision{}, false
		case <-co.ctx.Done():
			return workflow.RunDecision{}, false
		}
	}
}

// setStepFeedback stores the agent's redo feedback for a step (threaded into its
// re-dispatch prompt). Guarded — set on the main goroutine, read by workPrompt/briefPrompt.
func (co *coordinator) setStepFeedback(stepID, feedback string) {
	co.mu.Lock()
	co.stepFeedback[stepID] = feedback
	co.mu.Unlock()
}

// stepFeedbackFor returns the agent's redo feedback for a step (empty if none). Guarded.
func (co *coordinator) stepFeedbackFor(stepID string) string {
	co.mu.Lock()
	defer co.mu.Unlock()
	return co.stepFeedback[stepID]
}

// onDecision handles a run.decision the resident agent posts on the decision subject. It
// runs on a delivery goroutine, so it only touches mutex-guarded state — it never mutates
// co.run (single-writer; the main goroutine applies the decision). It ignores its own
// echoes and anything that is not a run.decision for a known step.
func (co *coordinator) onDecision(m sextant.Message) {
	if m.Frame.Author == co.c.ID() {
		return
	}
	dec, ok := workflow.ParseRunDecision(m.Frame.Record)
	if !ok || dec.Step == "" {
		return
	}
	co.mu.Lock()
	co.decisions[dec.Step] = dec
	co.mu.Unlock()
	wake(co.decCh)
}
