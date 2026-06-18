// Command sextant-violet runs violet — the operator's assistant — as a
// long-lived SDK client on the sextant bus (milestone goal.violet, TASK-159).
//
// It connects as ONE registered bus client under violet's OWN scoped credentials
// (never the principal's ambient creds — TASK-158), and runs three concurrent
// internal roles over a shared in-memory warm context: a haiku GATE that triages
// scoped, pre-filtered events; a sonnet HOME-MANAGER woken on significant ones
// that re-curates the operator's `home` projection + keeps the warm context
// fresh; and a haiku CONVERSATIONAL role that answers operator DMs instantly from
// that context. It also exposes an action surface (start a workflow / spawn a
// scoped agent) so a cold start needs no persistent crew.
//
// SECURITY: register violet with her own creds first —
//
//	sextant clients register violet --kind agent --store $STORE --out violet.creds
//
// — and pass --creds violet.creds here. The process never receives the
// operator's creds; any agent it spawns is minted a fresh scoped identity by the
// dispatcher (cmd/sextant-dispatch), so violet hands out no credentials.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/love-lena/sextant/internal/violet"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

func main() {
	fs := flag.NewFlagSet("sextant-violet", flag.ExitOnError)
	creds := fs.String("creds", os.Getenv("SEXTANT_CREDS"), "violet's own credentials file (its own bus identity; issue with `sextant clients register violet`)")
	store := fs.String("store", os.Getenv("SEXTANT_STORE"), "bus store dir for bus.json discovery")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	apiKey := fs.String("api-key", os.Getenv("ANTHROPIC_API_KEY"), "Anthropic API key for violet's model turns (or $ANTHROPIC_API_KEY)")
	operator := fs.String("operator", "", "the principal's bus client id (default: the bus's designated principal)")
	convModel := fs.String("conv-model", violet.ModelHaiku, "conversational (answer) model — fast")
	gateModel := fs.String("gate-model", violet.ModelHaiku, "gate (significance triage) model — cheap")
	deepModel := fs.String("deep-model", violet.ModelSonnet, "home-manager (deep refresh) model — capable")
	safety := fs.Duration("safety-interval", 15*time.Minute, "slow safety-net interval for the deep pass (the gate is the primary trigger)")
	stateDir := fs.String("state-dir", os.Getenv("VIOLET_STATE_DIR"), "directory for violet's durable substate (ack cursor for AC8 replay; default: a violet/ subdir under the sextant config root)")
	designate := fs.Bool("designate", false, "create/update the `assistant` designation artifact naming violet the live assistant (release-time, ADR-0039)")
	_ = fs.Parse(os.Args[1:])

	if *creds == "" {
		fatal("usage: sextant-violet --creds violet.creds --store DIR [--api-key K] [--operator ID] [--designate]")
	}
	if *apiKey == "" {
		fatal("no Anthropic API key (set --api-key or $ANTHROPIC_API_KEY) — violet drives models for her three roles")
	}
	// Gate point 1: the durable cursor MUST persist across a real restart. When
	// neither --state-dir nor $VIOLET_STATE_DIR is set, default to the persistent
	// per-user path (violet/ under the sextant config root) so the AC8 watermark
	// survives a restart with zero operator config.
	if *stateDir == "" {
		*stateDir = violet.DefaultStateDir()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := sextant.Connect(connCtx, sextant.Options{
		CredsPath:    *creds,
		URL:          *url,
		ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		fatal("connect: %v", err)
	}
	defer c.Close()

	if *designate {
		if err := designateAssistant(ctx, c); err != nil {
			fatal("designate assistant: %v", err)
		}
		logf("assistant designation set → violet (%s)", c.ID())
	}

	v := violet.New(violet.NewSDKAdapter(c), violet.NewModelClient(*apiKey, "", nil), violet.Config{
		OperatorID:     *operator,
		ConvModel:      *convModel,
		GateModel:      *gateModel,
		DeepModel:      *deepModel,
		SafetyInterval: *safety,
		StateDir:       *stateDir,
		Logf:           logf,
	})

	logf("violet up as %s; watching the operator DM + goals + artifact review + crew (scoped)", c.ID())
	if err := v.Run(ctx); err != nil {
		fatal("run: %v", err)
	}
	logf("violet: signalled; shutting down")
}

// designateAssistant creates or updates the `assistant` artifact (ADR-0039) that
// names violet the live assistant — what the dash + crew read. Release-time only.
func designateAssistant(ctx context.Context, c *sextant.Client) error {
	rec, _ := json.Marshal(struct {
		Type     string `json:"$type"`
		ClientID string `json:"client_id"`
		Name     string `json:"name"`
		Accent   string `json:"accent"`
	}{Type: "document", ClientID: c.ID(), Name: "violet", Accent: "#6a55e0"})

	if art, err := c.GetArtifact(ctx, "assistant"); err == nil {
		_, err = c.UpdateArtifact(ctx, "assistant", wire.Lexicon(rec), art.Revision)
		return err
	}
	_, err := c.CreateArtifact(ctx, "assistant", wire.Lexicon(rec))
	return err
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "sextant-violet: "+format+"\n", a...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "sextant-violet: "+format+"\n", a...)
	os.Exit(1)
}
