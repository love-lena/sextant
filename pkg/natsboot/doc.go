// Package natsboot starts a single-host NATS Server subprocess with
// JetStream enabled, applies the sextant stream and KV layout, and
// hands back a *Server that the caller can use to connect or stop.
//
// Spec: specs/components/nats.md, specs/protocols/bus-subjects.md.
// Plan: plans/bootstrap.md#M2.
package natsboot
