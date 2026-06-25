package violet

import "strings"

// significanceKeywords are the cheap pre-filter signals (handoff fix #2): a
// keyword test on the event text that runs BEFORE the gate LLM. Only a match
// reaches the haiku gate; obvious noise (WIP chatter, heartbeats) is dropped
// with no LLM turn, so the gate runs rarely, not per event. The list mirrors
// the curation skill's significance signals.
var significanceKeywords = []string{
	"review", "ready", "approv", "verdict", "sign-off", "signoff",
	"blocked", "goal", "criterion", "criteria", "merged", "gate",
	"waiting on you", "needs you", "waiting-on-you",
}

// prefilter reports whether an event is a gate candidate. operatorAuthored is
// always a candidate (a message from the principal is significant by default —
// the gate still confirms). Otherwise the event text must contain a
// significance keyword. The match is case-insensitive on the whole frame text.
//
// This is the cost gate: a busy bus is mostly WIP + status churn, and dropping
// it here means the haiku gate (and the sonnet deep pass behind it) never run
// on it — which is exactly what keeps answers fast under load (the single-loop
// gate over the firehose is the spike's live failure, Bugs #4).
func prefilter(text string, operatorAuthored bool) bool {
	if operatorAuthored {
		return true
	}
	lower := strings.ToLower(text)
	for _, kw := range significanceKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
