// Command quickstart is the runnable example from the mdbook getting-started
// guide. It connects to a bus as an issued identity, publishes a chat message,
// reads it back, shares a document artifact, and drains.
//
// Run it after `sextant up` and `sextant clients register --self`:
//
//	go run ./examples/quickstart -creds "$SEXTANT_CREDS" -store "$SEXTANT_STORE"
//
// (or pass -url instead of -store to point at the bus directly).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
)

func main() {
	creds := flag.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (issued by `sextant clients register`)")
	store := flag.String("store", os.Getenv("SEXTANT_STORE"), "bus discovery + key-material directory")
	url := flag.String("url", "", "bus URL (default: discovered from -store)")
	flag.Parse()
	if *creds == "" {
		log.Fatal("set -creds (or $SEXTANT_CREDS) to a credentials file from `sextant clients register`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Connect as our issued identity. The id — our frame author and registry
	//    key — is read from the credential; we never assert who we are.
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    *creds,
		URL:          *url,
		ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	fmt.Printf("connected as %s (%s)\n", c.DisplayName(), c.ID())

	// 2. Publish a chat message on a topic. The bus stamps the frame.
	topic := sx.TopicSubject("quickstart")
	hello, _ := json.Marshal(map[string]string{"$type": "chat.message", "text": "hello from the quickstart"})
	if err := c.Publish(ctx, topic, hello); err != nil {
		log.Fatalf("publish: %v", err)
	}

	// 3. Read it back from the start of the topic's retained history. The author
	//    on the frame is bus-stamped, not something the sender chose.
	frames, _, err := c.FetchMessages(ctx, topic, 0, 10)
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	for _, f := range frames {
		fmt.Printf("message from %s: %s\n", f.Author, f.Record)
	}

	// 4. Share a document artifact, then read it back with its bus-stamped revision.
	doc, _ := json.Marshal(map[string]string{"$type": "document", "title": "Quickstart", "body": "It works."})
	if _, err := c.CreateArtifact(ctx, "quickstart/notes", doc); err != nil {
		log.Fatalf("create artifact: %v", err)
	}
	got, err := c.GetArtifact(ctx, "quickstart/notes")
	if err != nil {
		log.Fatalf("get artifact: %v", err)
	}
	fmt.Printf("artifact %q rev %d: %s\n", got.Name, got.Revision, got.Record)

	// 5. Close drains cooperatively and goes offline — it does not retire the
	//    identity, which persists in the directory for next time.
	fmt.Println("done")
}
