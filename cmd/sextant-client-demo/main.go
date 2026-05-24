// sextant-client-demo is the M4 acceptance harness. It connects via
// pkg/client to a running NATS, subscribes to the requested subject,
// and prints every received envelope's ID, stream sequence, and kind
// until interrupted.
//
// Usage:
//
//	sextant-client-demo --config ~/.config/sextant/client.toml \
//	                    --subject 'agents.*.frames' \
//	                    --deliver-all
//
// Not part of the production daemon — sextantd (M5) drives the real
// production lifecycle via pkg/client directly.
//
// Plan: plans/bootstrap.md#M4
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/love-lena/sextant-initial/pkg/client"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextant-client-demo: %v", err)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", "", "client config TOML (default ~/.config/sextant/client.toml)")
		subject     = flag.String("subject", "agents.*.frames", "subject to subscribe to (wildcards allowed)")
		deliverAll  = flag.Bool("deliver-all", false, "replay every message currently in the stream before going live")
		fromSeq     = flag.Uint64("from-seq", 0, "if non-zero, resume from this JetStream stream sequence")
		maxMessages = flag.Int("max", 0, "exit after this many messages (0 = run until Ctrl+C)")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	cli, err := client.Connect(ctx, *configPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = cli.Close() }()

	var opts []client.SubscribeOption
	switch {
	case *fromSeq > 0:
		opts = append(opts, client.WithStartSeq(*fromSeq))
	case *deliverAll:
		opts = append(opts, client.WithDeliverAll())
	}

	ch, err := cli.Subscribe(ctx, *subject, opts...)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	fmt.Fprintf(os.Stderr, "subscribed to %s\n", *subject)

	count := 0
	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			count++
			if m.Err != nil {
				fmt.Fprintf(os.Stderr, "seq=%d subject=%s decode error: %v\n", m.StreamSeq, m.Subject, m.Err)
			} else {
				payloadPreview := truncate(string(m.Envelope.Payload), 80)
				fmt.Printf("seq=%d subject=%s id=%s kind=%s payload=%s\n",
					m.StreamSeq, m.Subject, m.Envelope.ID, m.Envelope.Kind, payloadPreview)
			}
			if err := m.Ack(); err != nil {
				fmt.Fprintf(os.Stderr, "ack error: %v\n", err)
			}
			if *maxMessages > 0 && count >= *maxMessages {
				return nil
			}
		}
	}
}

// truncate returns s capped to n bytes with an ellipsis if it was longer.
// JSON payloads in agent_frame can be large; the demo prints a short
// preview rather than the full body.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
