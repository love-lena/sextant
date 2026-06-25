// The Wire API subject scheme + operation names — internal plumbing mirroring
// protocol/wireapi. A client invokes an operation by making a NATS request to
// sx.api.<clientID>.<op>; the bus pushes deliveries to sx.deliver.<clientID>.<sub>
// and heartbeat echoes to sx.hb.<clientID>. The <clientID> token in a call
// subject is the call's author — the per-client credential only permits
// publishing under its own prefix, so the bus-stamped author is unforgeable.

// Subject-space prefixes.
export const API_PREFIX = "sx.api.";
export const DELIVER_PREFIX = "sx.deliver.";
export const HEARTBEAT_PREFIX = "sx.hb.";

// DRAIN_SUB_ID is the reserved sub-id for the cooperative-drain delivery on a
// client's push space (sx.deliver.<id>.drain).
export const DRAIN_SUB_ID = "drain";

// Operation names — the protocol's operations (protocol/methods.json) plus the
// reference-bus extensions (hello/heartbeat/principal/subscription.stop), which
// are bus plumbing, not core protocol ops. Mirrors wireapi.Op* constants.
export const OP = {
  messagePublish: "message.publish",
  messageRead: "message.read",
  messageSubscribe: "message.subscribe",
  subscriptionStop: "subscription.stop",
  artifactCreate: "artifact.create",
  artifactUpdate: "artifact.update",
  artifactGet: "artifact.get",
  artifactList: "artifact.list",
  artifactDelete: "artifact.delete",
  artifactWatch: "artifact.watch",
  clientsList: "clients.list",
  clientsRegister: "clients.register",
  clientsRetire: "clients.retire",
  clientsHello: "clients.hello",
  clientsHeartbeat: "clients.heartbeat",
  principalGet: "principal.get",
  principalSet: "principal.set",
  principalWatch: "principal.watch",
} as const;

// callSubject builds the request subject for clientID invoking op.
export function callSubject(clientID: string, op: string): string {
  return API_PREFIX + clientID + "." + op;
}

// deliverSubject builds the push-delivery subject for one subscription:
// sx.deliver.<clientID>.<subID>. The SDK subscribes to it BEFORE making the
// subscribe/watch call so no delivery races the subscription.
export function deliverSubject(clientID: string, subID: string): string {
  return DELIVER_PREFIX + clientID + "." + subID;
}

// heartbeatSubject builds the per-client heartbeat-echo subject: sx.hb.<clientID>.
export function heartbeatSubject(clientID: string): string {
  return HEARTBEAT_PREFIX + clientID;
}

// inboxPrefix is a client's private request/reply inbox prefix: _INBOX.<clientID>.
// The SDK sets it as the connection's custom inbox so its call replies land
// under it — the only inbox the per-client credential may subscribe to. No
// trailing dot: the NATS client appends ".<token>" itself. Mirrors
// wireapi.InboxPrefix.
export function inboxPrefix(clientID: string): string {
  return "_INBOX." + clientID;
}
