package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// deps is what tool handlers close over: the held connection, the display
// name cache, and the channel-delivery hub.
type deps struct {
	conn  *connManager
	names *nameCache
	hub   *channelHub
}

// toolDef binds an MCP tool to the protocol operation it exposes (ADR-0017).
// op is empty for declared extras — tools that are channel control rather
// than protocol verbs. channel marks push-stream verbs whose delivery rides
// the Claude Code channel instead of the tool result.
type toolDef struct {
	name     string
	op       string
	channel  bool
	register func(s *mcp.Server, d *deps)
}

// toolDefs is the whole MCP surface; TestMCPMatchesOperations pins it to
// protocol/methods.json in both directions.
var toolDefs = []toolDef{
	{name: "message_publish", op: "message.publish", register: registerMessagePublish},
	{name: "message_read", op: "message.read", register: registerMessageRead},
	{name: "message_subscribe", op: "message.subscribe", channel: true, register: registerMessageSubscribe},
	{name: "message_unsubscribe", register: registerMessageUnsubscribe},
	{name: "artifact_create", op: "artifact.create", register: registerArtifactCreate},
	{name: "artifact_update", op: "artifact.update", register: registerArtifactUpdate},
	{name: "artifact_get", op: "artifact.get", register: registerArtifactGet},
	{name: "artifact_list", op: "artifact.list", register: registerArtifactList},
	{name: "artifact_delete", op: "artifact.delete", register: registerArtifactDelete},
	{name: "clients_list", op: "clients.list", register: registerClientsList},
}

// excludedOps are operations deliberately not exposed as MCP tools, with the
// reason. Registration is a setup-time concern the CLI owns (the skill's
// recipe runs `sextant clients register --self` beside the session);
// artifact.watch slots in later as a second channel-delivered tool.
var excludedOps = map[string]string{
	"clients.register": "setup-time, CLI-owned (skill recipe)",
	"clients.retire":   "setup-time, CLI-owned",
	"artifact.watch":   "deferred; future channel-delivered tool",
}

// declaredExtras are tools that map to no protocol operation: channel
// control, the MCP analogue of Ctrl-C on `sextant subscribe`.
var declaredExtras = map[string]bool{
	"message_unsubscribe": true,
}

func registerTools(s *mcp.Server, d *deps) {
	for _, td := range toolDefs {
		td.register(s, d)
	}
}
