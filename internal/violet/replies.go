package violet

import (
	"context"
	"encoding/json"
	"fmt"
)

// RepliesSubject is the ONE published place where violet records every answer
// it sends to the operator. It is a bus topic that dash (TASK-160's view) and
// any interested crew member can follow. violet is the ONLY author on this
// subject (criterion 4 — authors as violet, never impersonates the operator or
// another client). The subject is under msg.topic so it is reachable as a
// normal bus topic.
//
// Design rationale: a dedicated subject (rather than the DM subject alone) lets
// TASK-160 render a unified "violet answered" feed without having to subscribe
// to every possible DM pair. violet's replies here are a curated projection of
// what she said — the record carries the original question sequence so the dash
// can link back to the question.
const RepliesSubject = "msg.topic.violet.replies"

// replyRecord is the shape published to RepliesSubject on each answered DM. It
// is a bus record (opaque content, wire.Lexicon) that the dash reads (TASK-160).
// The $type is "violet.reply" to distinguish it from a plain chat.message.
type replyRecord struct {
	Type    string `json:"$type"`
	Text    string `json:"text"`      // the trimmed reply text (≤250 chars)
	QuotSeq uint64 `json:"quotSeq"`   // sequence of the original operator DM
	Subject string `json:"dmSubject"` // the DM subject (for TASK-160 linkback)
}

// publishReply publishes the answer to TWO places:
//  1. the DM subject — the operator sees it in her DM thread (existing behaviour).
//  2. RepliesSubject — the ONE unified surface (AC8 / TASK-160 render side).
//
// SECURITY (criterion 4): both publishes use violet's own bus client (pub);
// violet never writes to a foreign subject or impersonates another client.
// The DM subject is violet's own two-party topic (set in Violet.dmSubject).
//
// This is the output-capture path (from the handoff): the wrapper owns both
// publishes; the model never calls publish (so a forgotten publish is
// structurally impossible).
func publishReply(ctx context.Context, pub publisher, dmSubject, replyText string, questionSeq uint64) error {
	// 1. DM subject — the operator's thread.
	dmRec := chatMessage(replyText)
	if _, err := pub.PublishMsg(ctx, dmSubject, dmRec); err != nil {
		return fmt.Errorf("violet: publish DM reply: %w", err)
	}

	// 2. Unified surface — RepliesSubject.
	rec := replyRecord{
		Type:    "violet.reply",
		Text:    replyText,
		QuotSeq: questionSeq,
		Subject: dmSubject,
	}
	b, _ := json.Marshal(rec)
	if _, err := pub.PublishMsg(ctx, RepliesSubject, b); err != nil {
		// Non-fatal: the DM reply already landed; the unified surface publish is
		// best-effort. Log but do not fail the answer — the operator got her reply.
		return fmt.Errorf("violet: publish unified reply surface (non-fatal): %w", err)
	}
	return nil
}
