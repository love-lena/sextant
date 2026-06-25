package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// kindWorkflowDef is the $type of a workflow-definition artifact: a declarative
// description of an agentic dev-workflow run (TASK-98). Anyone authors one as an
// artifact, the operator reviews it on the dash, then runs it with
// `sextant workflow run <name>` — which reads the artifact and launches the LLM
// orchestrator (docs/demos/agentic-dev-workflow.sh). The bus stays the source of
// truth for the definition; the command carries the operator's authority to spawn.
const kindWorkflowDef = "sextant.workflow.def/v1"

// workflowDef is the record an `sextant workflow run` reads from the named artifact.
type workflowDef struct {
	Type              string          `json:"$type"`
	Title             string          `json:"title,omitempty"`
	Task              string          `json:"task"`                        // what to build (required)
	Steps             json.RawMessage `json:"steps"`                       // the explicit pipeline (required) — see the playbook
	Base              string          `json:"base,omitempty"`              // base ref (default origin/main)
	Repo              string          `json:"repo,omitempty"`              // target repo path (default: cwd git root)
	OrchestratorModel string          `json:"orchestratorModel,omitempty"` // default claude-sonnet-4-6
	WorkerModel       string          `json:"workerModel,omitempty"`       // default claude-haiku-4-5
	Notes             string          `json:"notes,omitempty"`
}

func cmdWorkflow(args []string) {
	if len(args) < 1 {
		fatal("usage: sextant workflow run <name> [--dry-run]")
	}
	switch args[0] {
	case "run":
		workflowRun(args[1:])
	default:
		fatal("usage: sextant workflow run <name> [--dry-run]")
	}
}

// workflowRun reads the named workflow-def artifact and launches the orchestrator
// harness with the operator's authority. The artifact is the ergonomic, reviewable
// substitute for a pile of env vars: `sextant workflow run task62` and nothing else.
func workflowRun(args []string) {
	// The name may be the first positional (flags then follow it; Go's flag package
	// stops at the first non-flag, so pull it off before parsing) or come after flags.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("workflow run", flag.ExitOnError)
	cf := addConnFlags(fs)
	dry := fs.Bool("dry-run", false, "read + validate the workflow-def and print the plan, but don't launch")
	_ = fs.Parse(args)
	if name == "" {
		if rest := fs.Args(); len(rest) > 0 {
			name = rest[0]
		}
	}
	if name == "" {
		fatal("usage: sextant workflow run <name> [--dry-run]")
	}

	ctx := context.Background()
	c := cf.connect(ctx)
	defer func() { _ = c.Close() }()
	a, err := c.GetArtifact(ctx, name)
	if err != nil {
		fatal("read workflow %q: %v", name, err)
	}
	var def workflowDef
	if err := json.Unmarshal(a.Record, &def); err != nil {
		fatal("workflow %q is not a valid workflow-def: %v", name, err)
	}
	if def.Type != kindWorkflowDef {
		fatal("artifact %q is $type %q, not %s", name, def.Type, kindWorkflowDef)
	}
	if strings.TrimSpace(def.Task) == "" {
		fatal("workflow %q has no task", name)
	}
	var steps []json.RawMessage
	if len(def.Steps) > 0 {
		if err := json.Unmarshal(def.Steps, &steps); err != nil {
			fatal("workflow %q has an invalid steps list: %v", name, err)
		}
	}
	if len(steps) == 0 {
		fatal("workflow %q has no steps (the def must declare an explicit pipeline; see the orchestrator playbook)", name)
	}
	if def.OrchestratorModel == "" {
		def.OrchestratorModel = "claude-sonnet-4-6"
	}
	if def.WorkerModel == "" {
		def.WorkerModel = "claude-haiku-4-5"
	}

	repo := def.Repo
	if repo == "" {
		repo = gitTopLevel()
	}
	if repo == "" {
		fatal("could not resolve a repo: run from inside the target git repo, or set \"repo\" in the workflow-def")
	}
	harness := filepath.Join(repo, "docs", "demos", "agentic-dev-workflow.sh")
	if _, err := os.Stat(harness); err != nil {
		fatal("orchestrator harness not found at %s (v1 runs from a sextant checkout): %v", harness, err)
	}

	store := *cf.store // the resolved --store (defaults to defaultStore()); the harness must use the SAME bus the command connected with
	base := def.Base
	if base == "" {
		base = "origin/main"
	}

	fmt.Printf("workflow %q (rev %d)\n  task:   %s\n  steps:  %d\n  repo:   %s\n  base:   %s\n  models: orchestrator=%s worker=%s\n  store:  %s\n",
		name, a.Revision, wfFirstLine(def.Task), len(steps), repo, base, def.OrchestratorModel, def.WorkerModel, store)
	if *dry {
		fmt.Println("(dry-run: validated; not launching)")
		return
	}

	env := append(
		os.Environ(),
		"SEXTANT_STORE="+store,
		"WF_ID="+name,
		"WF_BASE="+base,
		"WF_ORCH_MODEL="+def.OrchestratorModel,
		"WF_CLAUDE_MODEL="+def.WorkerModel,
		"WF_STEPS="+string(def.Steps),
	)
	cmd := exec.Command("bash", harness, "run", def.Task)
	cmd.Env = env
	cmd.Dir = repo
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("workflow run: %v", err)
	}
}

func gitTopLevel() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func wfFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
