// Package mcpserver implements the in-process MCP server that sextantd
// exposes to SDK sidecars over Streamable HTTP and to local CLI/TUI
// callers over stdio framed on a Unix socket.
//
// The server is the load-bearing surface that turns §9c capability
// descoping from a convention into an enforced contract. The HTTP
// transport runs `auth.RequireBearerToken` middleware against the M5 CA
// on every request; the tool dispatcher reads the resulting TokenInfo
// out of the request context and rejects calls whose JWT lacks the
// tool's declared capability. The stdio transport bypasses JWT — the
// Unix socket's `0600` mode is the operator-only trust boundary per
// §10b.
//
// One MCP Server instance is shared between both transports. Tool
// handlers receive a Caller value carrying identity + capability list so
// the same handler code paths drive both surfaces.
//
// Plan: plans/bootstrap.md#M10
// Spec: specs/architecture.md §9c, specs/components/sextantd.md
//
//	§"MCP server"
package mcpserver
