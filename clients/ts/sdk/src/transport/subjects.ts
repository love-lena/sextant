// User-space subject helpers (the messages convention) — pure, no I/O. Mirrors
// protocol/sx (TopicSubject, ClientSubject, DMSubject). These are naming
// conventions over the messages space (msg.*), not bus constructs.

// MESSAGE_PREFIX is the root of the messages subject space (msg.>). A publish
// subject must start with it.
export const MESSAGE_PREFIX = "msg.";

// topicSubject is the subject for a named topic: msg.topic.<name>. A topic is a
// shared room many clients publish to and subscribe to.
export function topicSubject(name: string): string {
  return MESSAGE_PREFIX + "topic." + name;
}

// clientSubject is a client's inbox subject: msg.client.<id>. An inbox is a
// one-way mailbox the owner auto-subscribes to on connect.
export function clientSubject(id: string): string {
  return MESSAGE_PREFIX + "client." + id;
}

// dmSubject is the subject for a direct message between two clients: a topic
// with exactly two participants (ADR-0034). The two ULIDs are sorted so both
// sides compute the identical subject from their own and the peer's id.
export function dmSubject(a: string, b: string): string {
  let lo = a;
  let hi = b;
  if (hi < lo) {
    lo = b;
    hi = a;
  }
  return topicSubject("dm." + lo + "." + hi);
}
