package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant/pkg/wire"
)

// toolError surfaces err as the tool result (an MCP tool error, not a
// protocol error): the agent reads it and can act on it — including the
// pre-connection errors whose text is the recovery recipe.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// jsonResult marshals v as the tool result text.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

type publishArgs struct {
	Subject string         `json:"subject" jsonschema:"the messages-space subject to publish on (msg.topic.<name> for a topic, msg.client.<id> for a DM)"`
	Record  map[string]any `json:"record" jsonschema:"the lexicon record, e.g. {\"$type\":\"chat.message\",\"text\":\"...\"}"`
}

func registerMessagePublish(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "message_publish",
		Description: "Publish a lexicon record to a subject on the sextant bus. Also the reply path for inbound channel events.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args publishArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		if err := c.Publish(ctx, args.Subject, mustRaw(args.Record)); err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"published": args.Subject})
		return res, nil, err
	})
}

type readArgs struct {
	Subject string `json:"subject" jsonschema:"exact subject or wildcard (e.g. msg.topic.plan or msg.>)"`
	Since   uint64 `json:"since,omitempty" jsonschema:"cursor to read from; 0 = start of retained history; pass the previous next_cursor to continue without gaps"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max messages to return (default 100)"`
}

// readMessage is one message_read result entry: the bus-stamped frame plus
// author_display resolved by this server — beside the frame, never inside it.
type readMessage struct {
	wire.Frame
	AuthorDisplay string `json:"author_display,omitempty"`
}

func registerMessageRead(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "message_read",
		Description: "Pull a batch of retained messages from a subject by cursor (read-since). The portable way to catch up; pair with message_subscribe to follow live.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args readArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 100
		}
		frames, next, err := c.FetchMessages(ctx, args.Subject, args.Since, limit)
		if err != nil {
			return toolError(err), nil, nil
		}
		msgs := make([]readMessage, len(frames))
		for i, f := range frames {
			msgs[i] = readMessage{Frame: f, AuthorDisplay: d.names.displayName(ctx, f.Author)}
		}
		res, err := jsonResult(map[string]any{"messages": msgs, "next_cursor": next})
		return res, nil, err
	})
}

type artifactCreateArgs struct {
	Name   string         `json:"name" jsonschema:"artifact name"`
	Record map[string]any `json:"record" jsonschema:"the lexicon record, e.g. {\"$type\":\"document\",\"title\":\"...\",\"body\":\"...\"}"`
}

func registerArtifactCreate(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "artifact_create",
		Description: "Create a named artifact (shared mutable state with revisions).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args artifactCreateArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		rev, err := c.CreateArtifact(ctx, args.Name, wire.Lexicon(mustRaw(args.Record)))
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"name": args.Name, "revision": rev})
		return res, nil, err
	})
}

type artifactUpdateArgs struct {
	Name        string         `json:"name" jsonschema:"artifact name"`
	Record      map[string]any `json:"record" jsonschema:"the full replacement record"`
	ExpectedRev uint64         `json:"expected_rev" jsonschema:"the revision this update is based on (compare-and-swap; get the artifact first)"`
}

func registerArtifactUpdate(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "artifact_update",
		Description: "Update an artifact with compare-and-swap on expected_rev.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args artifactUpdateArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		rev, err := c.UpdateArtifact(ctx, args.Name, wire.Lexicon(mustRaw(args.Record)), args.ExpectedRev)
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"name": args.Name, "revision": rev})
		return res, nil, err
	})
}

type artifactNameArgs struct {
	Name string `json:"name" jsonschema:"artifact name"`
}

func registerArtifactGet(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "artifact_get",
		Description: "Get an artifact's record and revision by name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args artifactNameArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		a, err := c.GetArtifact(ctx, args.Name)
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(a)
		return res, nil, err
	})
}

type emptyArgs struct{}

func registerArtifactList(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "artifact_list",
		Description: "List artifacts (name, revision, timestamps — no records).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args emptyArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		infos, err := c.ListArtifacts(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"artifacts": infos})
		return res, nil, err
	})
}

func registerArtifactDelete(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "artifact_delete",
		Description: "Delete an artifact by name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args artifactNameArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		if err := c.DeleteArtifact(ctx, args.Name); err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"deleted": args.Name})
		return res, nil, err
	})
}

func registerClientsList(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "clients_list",
		Description: "List registered clients: id, display name, kind, online presence.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args emptyArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		clients, err := c.ListClients(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"clients": clients})
		return res, nil, err
	})
}

// errNotSubscribed keeps unsubscribe honest about what it can stop.
func errNotSubscribed(subject string, active []string) error {
	return fmt.Errorf("not subscribed to %q; active subscriptions: %v", subject, active)
}

// mustRaw re-marshals a validated tool argument; marshaling a map[string]any
// that just unmarshaled cannot fail.
func mustRaw(v map[string]any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
