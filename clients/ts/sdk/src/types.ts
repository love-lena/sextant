// Shared public types for the Sextant TS SDK. These mirror the SEMANTICS of the
// Go SDK's surface (clients/go/sdk) — they are not a Go-idiom transliteration:
// where Go returns a struct, the TS surface uses a plain object with the natural
// TS shape (Date for timestamps, number for revisions/sequences).

import type { Frame } from "./wire/frame.js";

// JSONValue is the opaque user payload a frame carries: a lexicon (ADR-0005,
// ADR-0016). It is the same content model whether the frame is a message in
// flight or an artifact at rest.
//
// bigint is included because the canonical-JSON rule (FORMAT.md rule 4) requires
// integers beyond IEEE-754 double precision (> 2^53-1) to survive with their
// EXACT digits. The codec parses such integers to bigint and serializes a bigint
// back to bare digits, so a record round-trips byte-faithfully through the wire.
// A caller that never carries such an integer never sees a bigint.
export type JSONValue =
  | null
  | boolean
  | number
  | bigint
  | string
  | JSONValue[]
  | { [key: string]: JSONValue };

// Message is a received message: the decoded frame plus the bus-stamped metadata
// the receiver trusts (the JetStream-stamped clock and stream sequence).
export interface Message {
  frame: Frame;
  subject: string;
  busTime: Date; // JetStream-stamped; the trusted clock
  sequence: number;
}

// Artifact is a named, versioned unit of durable shared work. Its record is a
// lexicon — the same content model as a message's (ADR-0005, ADR-0016).
export interface Artifact {
  name: string;
  record: JSONValue;
  revision: number;
  created: Date;
}

// ArtifactInfo is one entry in the artifacts directory: an artifact's name and
// bus-stamped metadata, but NOT its record (discovery, not contents).
export interface ArtifactInfo {
  name: string;
  revision: number;
  created: Date;
  updated: Date;
}

// ArtifactChange is a change delivered to a watchArtifact handler: the artifact
// at this revision, plus whether the change was a deletion. On a delete the
// record is empty and deleted is true.
export interface ArtifactChange extends Artifact {
  deleted: boolean;
}

// ClientInfo is one entry in the clients directory (ADR-0020): a bus-issued
// identity joined with live presence. Listed online or offline, from issuance
// until retire.
export interface ClientInfo {
  id: string;
  displayName: string;
  kind: string;
  epoch: number;
  online: boolean;
  issuedAt: Date;
}

// IssuedClient is the result of minting a new identity: its bus-generated ULID
// and its NATS credential text (JWT+seed). The creds are SECRET material — write
// them to a 0600 file and hand them to the new client.
export interface IssuedClient {
  id: string;
  creds: string; // full NATS .creds text; SECRET
}

// HeartbeatState is the SDK's in-process view of its own liveness round-trip
// (mirrors Go's HeartbeatState): the last beat sent and the last echo the bus
// pushed back. Fresh reports whether the last echo is within the freshness
// window — the watchdog's "push path is live" signal.
export interface HeartbeatState {
  lastBeatSeq: number;
  lastEchoSeq: number;
  lastEchoAt: Date;
  fresh: boolean;
}
