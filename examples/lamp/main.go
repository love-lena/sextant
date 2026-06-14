// Command lamp puts a small ambient lamp on the bus, as a `document` artifact
// named `lamp`. It doesn't do anything else. It's just on.
//
// Run after `sextant up` and `sextant clients register --self`:
//
//	go run ./examples/lamp -creds "$SEXTANT_CREDS" -store "$SEXTANT_STORE"
//
// Idempotent: if a lamp is already on the bus, this leaves it alone. The lamp
// is a regular artifact — read it, replace it, fork it. Your bus, your lamp.
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

	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
)

const lampArt = "```\n" +
	"            .   *   .   *   .\n" +
	"         *       ___       *\n" +
	"           .    /   \\    .\n" +
	"        *      |     |      *\n" +
	"           .    \\___/    .\n" +
	"         *        |        *\n" +
	"            .     |     .\n" +
	"                  |\n" +
	"                 _|_\n" +
	"                |___|\n" +
	"```\n"

const lampBody = "A small ambient lamp for the bus. Brass base, paper shade, " +
	"on a permanent low setting. Doesn't do anything; just on.\n\n" + lampArt

func main() {
	creds := flag.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (issued by `sextant clients register`)")
	store := flag.String("store", os.Getenv("SEXTANT_STORE"), "bus discovery + key-material directory")
	url := flag.String("url", "", "bus URL (default: discovered from -store)")
	flag.Parse()
	if *creds == "" {
		log.Fatal("set -creds (or $SEXTANT_CREDS) to a credentials file from `sextant clients register`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    *creds,
		URL:          *url,
		ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// A lamp is a lamp. If one's already on the bus, leave it.
	if got, err := c.GetArtifact(ctx, "lamp"); err == nil {
		fmt.Printf("lamp already on the bus (rev %d). nothing to do.\n", got.Revision)
		return
	}

	record, _ := json.Marshal(map[string]string{
		"$type": "document",
		"title": "Lamp",
		"body":  lampBody,
	})
	rev, err := c.CreateArtifact(ctx, "lamp", record)
	if err != nil {
		log.Fatalf("place lamp: %v", err)
	}
	fmt.Printf("lamp placed on the bus as artifact %q (rev %d)\n", "lamp", rev)
}
