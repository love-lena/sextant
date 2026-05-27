package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/sextantd"
)

// newInitCmd wires `sextant init`. Idempotent — each step inspects the
// filesystem first and skips when state is already correct. `--force`
// re-generates every file (CA included, which DESTROYS already-issued
// JWTs; the prompt is on the operator). `--check` is a read-only dry
// run that exits non-zero if the install is incomplete.
//
// init is a top-level singleton per `feat-cli-resource-verb-cleanup`
// (verb on the sextant install itself, not a resource noun).
func newInitCmd() *cobra.Command {
	var force, check bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "First-run setup: CA + config + data dirs + default template",
		Long: `Creates the config and data directories, generates the signing CA,
writes sextantd.toml + client.toml + operator.creds, and seeds default
templates.

Re-running is idempotent: every step skips when state is already correct.
--force regenerates every file, including the CA (which invalidates any
JWTs already issued).
--check is a read-only dry-run; exit 0 if everything is in place, exit 2
if any file is missing or broken. Useful for CI and pre-flight checks.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if force && check {
				return errUserUsage("--force and --check are mutually exclusive")
			}
			cfgPath, dataPath, err := resolveInitPaths(globalFlags.configDir, globalFlags.dataDir)
			if err != nil {
				return err
			}
			return doInit(cmd.Context(), cmd.OutOrStdout(), initOptions{
				ConfigDir: cfgPath,
				DataDir:   dataPath,
				Force:     force,
				Check:     check,
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "regenerate every file even if present")
	cmd.Flags().BoolVar(&check, "check", false, "dry-run: report what init would do without writing")
	return cmd
}

type initOptions struct {
	ConfigDir string
	DataDir   string
	Force     bool
	Check     bool
}

func resolveInitPaths(configFlag, dataFlag string) (configDir, dataDir string, err error) {
	if configFlag == "" || dataFlag == "" {
		c, d, derr := sextantd.DefaultPaths()
		if derr != nil {
			return "", "", derr
		}
		if configFlag == "" {
			configFlag = c
		}
		if dataFlag == "" {
			dataFlag = d
		}
	}
	configFlag, err = absPath(configFlag)
	if err != nil {
		return "", "", err
	}
	dataFlag, err = absPath(dataFlag)
	if err != nil {
		return "", "", err
	}
	return configFlag, dataFlag, nil
}

// stepOutcome buckets each step's contribution to the final summary line.
// Every step contributes exactly one outcome: either it acted (Written) or
// it found state already correct (Satisfied). In --check mode the same
// step reports WouldWrite or OK respectively, or WouldError if the
// existing state is corrupt/half-installed.
type stepOutcome int

const (
	outcomeSatisfied  stepOutcome = iota // file existed and validated; nothing done
	outcomeWritten                       // file was created or regenerated this run
	outcomeOK                            // --check: state is correct (no-op)
	outcomeWouldWrite                    // --check: state is missing, init would create it
	outcomeWouldError                    // --check: state is broken; init would fail without --force
)

// stepReport aggregates outcomes across all init steps so we can print a
// single summary line at the end. Errors collected here in --check mode
// are not fatal mid-run; we gather them all and signal a non-zero exit
// only after the full report has been printed.
type stepReport struct {
	written      int
	satisfied    int
	wouldWrite   int
	ok           int
	wouldError   int
	missingPaths []string // --check: paths that would be written or fixed
	brokenPaths  []string // --check: paths that exist but are invalid
}

func (r *stepReport) total() int {
	return r.written + r.satisfied + r.wouldWrite + r.ok + r.wouldError
}

func (r *stepReport) record(out stepOutcome, label, path string) {
	switch out {
	case outcomeSatisfied:
		r.satisfied++
	case outcomeWritten:
		r.written++
	case outcomeOK:
		r.ok++
	case outcomeWouldWrite:
		r.wouldWrite++
		r.missingPaths = append(r.missingPaths, formatStepPath(label, path))
	case outcomeWouldError:
		r.wouldError++
		r.brokenPaths = append(r.brokenPaths, formatStepPath(label, path))
	}
}

func formatStepPath(label, path string) string {
	if path == "" {
		return label
	}
	return label + " (" + path + ")"
}

// errCheckFailures signals that `sextant init --check` found missing or
// broken state. It is mapped to exitSystem (2) by exitCodeFor — same
// behavior as doctor failures, since the two tools serve a similar
// gate-keeping role.
var errCheckFailures = errors.New("init: check failed — installation is incomplete or broken")

// doInit runs the init steps in order, writing progress to w. In normal
// mode each step writes the missing files; in --check mode each step is
// read-only and the function returns errCheckFailures if anything is
// missing or broken.
func doInit(_ context.Context, w io.Writer, opts initOptions) error {
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	var rep stepReport

	// 1. Config dir (0700).
	if err := ensureDir(w, &rep, "config-dir", opts.ConfigDir, 0o700, opts.Check); err != nil {
		return err
	}

	// 2. Data dirs (0750).
	dataDirs := []string{
		opts.DataDir,
		filepath.Join(opts.DataDir, "nats"),
		filepath.Join(opts.DataDir, "clickhouse"),
		filepath.Join(opts.DataDir, "shipper-buffer"),
		filepath.Join(opts.DataDir, "test"),
	}
	for _, d := range dataDirs {
		if err := ensureDir(w, &rep, "data-dir", d, 0o750, opts.Check); err != nil {
			return err
		}
	}

	// 3. CA keypair.
	if err := stepCA(w, &rep, cfg.CA.KeyPath, cfg.CA.PubPath, opts.Force, opts.Check); err != nil {
		return err
	}

	// 4. Operator NATS creds.
	if err := stepOperatorCreds(w, &rep, cfg.NATS.OperatorCreds, opts.Force, opts.Check); err != nil {
		return err
	}

	// 5. ClickHouse password file.
	if err := stepClickHousePassword(w, &rep, cfg.ClickHouse.PasswordFile, opts.Force, opts.Check); err != nil {
		return err
	}

	// 6. sextantd.toml.
	sextantdTomlPath := filepath.Join(opts.ConfigDir, "sextantd.toml")
	if err := stepSextantdConfig(w, &rep, sextantdTomlPath, cfg, opts.Force, opts.Check); err != nil {
		return err
	}

	// 7. client.toml.
	if err := stepClientConfig(w, &rep, cfg.Paths.ClientConfig, cfg.NATS.OperatorCreds, opts.Force, opts.Check); err != nil {
		return err
	}

	// 8. shipper.toml.
	shipperTomlPath := filepath.Join(opts.ConfigDir, "shipper.toml")
	if err := stepShipperConfig(w, &rep, shipperTomlPath, opts.ConfigDir, opts.DataDir, opts.Force, opts.Check); err != nil {
		return err
	}

	// 9. Templates dir + default.toml.
	if err := ensureDir(w, &rep, "templates-dir", cfg.Paths.TemplatesDir, 0o700, opts.Check); err != nil {
		return err
	}
	if err := stepDefaultTemplate(w, &rep, cfg.Paths.TemplatesDir, opts.Force, opts.Check); err != nil {
		return err
	}

	if opts.Check {
		printCheckSummary(w, &rep)
		if rep.wouldWrite > 0 || rep.wouldError > 0 {
			return errCheckFailures
		}
		return nil
	}

	printRunSummary(w, &rep)

	// macOS-only postamble: steer operators to `make install`. Plain
	// `cp bin/* ~/.local/bin/` stamps com.apple.provenance onto the
	// destination, and Gatekeeper SIGKILLs the cp'd binary on
	// invocation (exit 137, no stderr — silent kill). `make install`
	// uses /usr/bin/install which sidesteps the xattr.
	// Issue: docs-install-via-make-install-not-cp
	if runtime.GOOS == "darwin" {
		println(w, "")
		println(w, "note: sextant binaries should be installed via `make install` (not plain cp).")
		println(w, "macOS Gatekeeper SIGKILLs cp'd Go binaries due to the com.apple.provenance")
		println(w, "xattr. `make install` uses /usr/bin/install which avoids the issue.")
	}
	return nil
}

// printRunSummary writes the one-line summary that closes a normal init
// run. Two forms: a fresh install that wrote everything, or an idempotent
// rerun that already had what it needed.
//
// Format:
//
//	init: N/N steps written
//	init: N/N steps already satisfied, W written, B regenerated (use --force to regenerate)
//
// The trailing hint about --force is only useful on idempotent reruns
// (W=0, B=0), so we condition it on that case.
func printRunSummary(w io.Writer, r *stepReport) {
	total := r.total()
	switch {
	case r.satisfied == 0 && r.written > 0:
		// Pure fresh install.
		printf(w, "init: %d/%d steps written\n", r.written, total)
	case r.satisfied == total:
		// Pure idempotent rerun.
		printf(w, "init: %d/%d steps already satisfied, 0 written (use --force to regenerate)\n", r.satisfied, total)
	default:
		// Mixed: partial fill-in.
		printf(w, "init: %d/%d steps already satisfied, %d written\n", r.satisfied, total, r.written)
	}
}

// printCheckSummary writes the summary for `sextant init --check`. The
// per-step lines have already been printed; this caps the report with
// the headline numbers and lists the missing/broken paths so an
// operator (or CI script) can see exactly what's wrong.
func printCheckSummary(w io.Writer, r *stepReport) {
	total := r.total()
	if r.wouldWrite == 0 && r.wouldError == 0 {
		printf(w, "init: check: ok — %d/%d steps in place\n", r.ok, total)
		return
	}
	printf(w, "init: check: %d/%d in place, %d would-write, %d would-error\n", r.ok, total, r.wouldWrite, r.wouldError)
	if len(r.missingPaths) > 0 {
		sort.Strings(r.missingPaths)
		printf(w, "init: check: missing: %s\n", strings.Join(r.missingPaths, ", "))
	}
	if len(r.brokenPaths) > 0 {
		sort.Strings(r.brokenPaths)
		printf(w, "init: check: broken: %s\n", strings.Join(r.brokenPaths, ", "))
	}
}

func ensureDir(w io.Writer, rep *stepReport, label, path string, mode os.FileMode, check bool) error {
	st, err := os.Stat(path)
	switch {
	case err == nil:
		if !st.IsDir() {
			if check {
				printf(w, "-> %s: would-error (exists but is not a directory): %s\n", label, path)
				rep.record(outcomeWouldError, label, path)
				return nil
			}
			return fmt.Errorf("%s exists and is not a directory: %s", label, path)
		}
		if check {
			printf(w, "-> %s: ok %s\n", label, path)
			rep.record(outcomeOK, label, path)
			return nil
		}
		// Best-effort: align mode if it drifted.
		if st.Mode().Perm() != mode {
			if err := os.Chmod(path, mode); err != nil {
				return fmt.Errorf("%s chmod %s: %w", label, path, err)
			}
		}
		printf(w, "-> %s: existing %s\n", label, path)
		rep.record(outcomeSatisfied, label, path)
		return nil
	case os.IsNotExist(err):
		if check {
			printf(w, "-> %s: would-write %s\n", label, path)
			rep.record(outcomeWouldWrite, label, path)
			return nil
		}
		if err := os.MkdirAll(path, mode); err != nil {
			return fmt.Errorf("%s mkdir %s: %w", label, path, err)
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("%s chmod %s: %w", label, path, err)
		}
		printf(w, "-> %s: created %s\n", label, path)
		rep.record(outcomeWritten, label, path)
		return nil
	default:
		return fmt.Errorf("%s stat %s: %w", label, path, err)
	}
}

func stepCA(w io.Writer, rep *stepReport, keyPath, pubPath string, force, check bool) error {
	keyExists := fileExists(keyPath)
	pubExists := fileExists(pubPath)

	switch {
	case keyExists && pubExists && !force:
		// Sanity: parse the existing pair so we don't silently sit on a
		// corrupted CA.
		if _, err := authjwt.LoadCA(keyPath, pubPath); err != nil {
			if check {
				printf(w, "-> ca: would-error (existing CA failed validation: %v)\n", err)
				rep.record(outcomeWouldError, "ca", keyPath)
				return nil
			}
			return fmt.Errorf("ca: existing CA failed validation: %w (re-run with --force to regenerate)", err)
		}
		if check {
			println(w, "-> ca: ok")
			rep.record(outcomeOK, "ca", keyPath)
		} else {
			println(w, "-> ca: existing")
			rep.record(outcomeSatisfied, "ca", keyPath)
		}
		return nil
	case keyExists != pubExists && !force:
		if check {
			printf(w, "-> ca: would-error (half-installed: key=%v pub=%v)\n", keyExists, pubExists)
			rep.record(outcomeWouldError, "ca", keyPath)
			return nil
		}
		return fmt.Errorf("ca: half-installed (key=%v pub=%v); re-run with --force", keyExists, pubExists)
	}

	if check {
		printf(w, "-> ca: would-write %s + %s\n", keyPath, pubPath)
		rep.record(outcomeWouldWrite, "ca", keyPath)
		return nil
	}

	privPEM, pubPEM, err := authjwt.GenerateCA()
	if err != nil {
		return fmt.Errorf("ca: generate: %w", err)
	}
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		return fmt.Errorf("ca: write %s: %w", keyPath, err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil { //nolint:gosec // ca.pub is intentionally world-readable
		return fmt.Errorf("ca: write %s: %w", pubPath, err)
	}
	verb := "generated"
	if keyExists || pubExists {
		verb = "regenerated"
	}
	printf(w, "-> ca: %s\n", verb)
	rep.record(outcomeWritten, "ca", keyPath)
	return nil
}

func stepOperatorCreds(w io.Writer, rep *stepReport, path string, force, check bool) error {
	if !force && fileExists(path) {
		if _, err := sextantd.ReadOperatorCreds(path); err != nil {
			if check {
				printf(w, "-> operator-creds: would-error (%v)\n", err)
				rep.record(outcomeWouldError, "operator-creds", path)
				return nil
			}
			return fmt.Errorf("operator-creds: existing file failed validation: %w (re-run with --force)", err)
		}
		if check {
			println(w, "-> operator-creds: ok")
			rep.record(outcomeOK, "operator-creds", path)
		} else {
			println(w, "-> operator-creds: existing")
			rep.record(outcomeSatisfied, "operator-creds", path)
		}
		return nil
	}
	if check {
		printf(w, "-> operator-creds: would-write %s\n", path)
		rep.record(outcomeWouldWrite, "operator-creds", path)
		return nil
	}
	preExisted := fileExists(path)
	pw, err := sextantd.GenerateOperatorPassword()
	if err != nil {
		return fmt.Errorf("operator-creds: %w", err)
	}
	if err := sextantd.WriteOperatorCreds(path, sextantd.OperatorCreds{User: "operator", Password: pw}); err != nil {
		return err
	}
	verb := "generated"
	if preExisted && force {
		verb = "regenerated"
	}
	printf(w, "-> operator-creds: %s\n", verb)
	rep.record(outcomeWritten, "operator-creds", path)
	return nil
}

func stepClickHousePassword(w io.Writer, rep *stepReport, path string, force, check bool) error {
	if !force && fileExists(path) {
		if _, err := sextantd.ReadPasswordFile(path); err != nil {
			if check {
				printf(w, "-> clickhouse-password: would-error (%v)\n", err)
				rep.record(outcomeWouldError, "clickhouse-password", path)
				return nil
			}
			return fmt.Errorf("clickhouse-password: existing failed validation: %w", err)
		}
		if check {
			println(w, "-> clickhouse-password: ok")
			rep.record(outcomeOK, "clickhouse-password", path)
		} else {
			println(w, "-> clickhouse-password: existing")
			rep.record(outcomeSatisfied, "clickhouse-password", path)
		}
		return nil
	}
	if check {
		printf(w, "-> clickhouse-password: would-write %s\n", path)
		rep.record(outcomeWouldWrite, "clickhouse-password", path)
		return nil
	}
	pw, err := sextantd.GenerateOperatorPassword()
	if err != nil {
		return fmt.Errorf("clickhouse-password: %w", err)
	}
	if err := sextantd.WritePasswordFile(path, pw); err != nil {
		return err
	}
	println(w, "-> clickhouse-password: generated")
	rep.record(outcomeWritten, "clickhouse-password", path)
	return nil
}

func stepSextantdConfig(w io.Writer, rep *stepReport, path string, cfg sextantd.Config, force, check bool) error {
	if !force && fileExists(path) {
		// Parse it to ensure it loads cleanly.
		if _, err := sextantd.LoadConfig(path); err != nil {
			if check {
				printf(w, "-> sextantd.toml: would-error (%v)\n", err)
				rep.record(outcomeWouldError, "sextantd.toml", path)
				return nil
			}
			return fmt.Errorf("sextantd.toml: existing file failed to parse: %w (re-run with --force)", err)
		}
		if check {
			println(w, "-> sextantd.toml: ok")
			rep.record(outcomeOK, "sextantd.toml", path)
		} else {
			println(w, "-> sextantd.toml: existing")
			rep.record(outcomeSatisfied, "sextantd.toml", path)
		}
		return nil
	}
	if check {
		printf(w, "-> sextantd.toml: would-write %s\n", path)
		rep.record(outcomeWouldWrite, "sextantd.toml", path)
		return nil
	}
	if err := sextantd.SaveConfig(path, cfg); err != nil {
		return err
	}
	println(w, "-> sextantd.toml: written")
	rep.record(outcomeWritten, "sextantd.toml", path)
	return nil
}

func stepClientConfig(w io.Writer, rep *stepReport, path, credsPath string, force, check bool) error {
	if !force && fileExists(path) {
		if check {
			println(w, "-> client.toml: ok")
			rep.record(outcomeOK, "client.toml", path)
		} else {
			println(w, "-> client.toml: existing")
			rep.record(outcomeSatisfied, "client.toml", path)
		}
		return nil
	}
	if check {
		printf(w, "-> client.toml: would-write %s\n", path)
		rep.record(outcomeWouldWrite, "client.toml", path)
		return nil
	}
	body := buildClientToml(credsPath)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("client.toml: write %s: %w", path, err)
	}
	println(w, "-> client.toml: written")
	rep.record(outcomeWritten, "client.toml", path)
	return nil
}

// buildClientToml renders a fresh client.toml. We hand-write the TOML so
// every comment from the spec is preserved verbatim — the file is
// operator-facing and a generated, blank document is confusing.
//
// The default port 4222 is a placeholder: the M5 daemon binds an
// auto-allocated port and records it in runtime.json. `sextant doctor`
// prefers runtime.json when both are present.
func buildClientToml(credsPath string) string {
	return fmt.Sprintf(`# sextant client.toml — written by `+"`sextant init`"+`.
# See specs/components/client-libraries.md §"Config file".

[nats]
url = "nats://127.0.0.1:4222"

[operator]
user        = "operator"
password    = ""
creds_path  = %q

[client]
connect_timeout = "10s"
request_timeout = "30s"
log_level       = "info"
`, credsPath)
}

// stepShipperConfig writes shipper.toml — config consumed by
// `sextant-shipper`. Empty NATS / ClickHouse addresses default to
// the live values from runtime.json at start time; the file is
// hand-written so comments survive a TOML round-trip.
func stepShipperConfig(w io.Writer, rep *stepReport, path, configDir, dataDir string, force, check bool) error {
	if !force && fileExists(path) {
		if check {
			println(w, "-> shipper.toml: ok")
			rep.record(outcomeOK, "shipper.toml", path)
		} else {
			println(w, "-> shipper.toml: existing")
			rep.record(outcomeSatisfied, "shipper.toml", path)
		}
		return nil
	}
	if check {
		printf(w, "-> shipper.toml: would-write %s\n", path)
		rep.record(outcomeWouldWrite, "shipper.toml", path)
		return nil
	}
	body := buildShipperToml(configDir, dataDir)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("shipper.toml: write %s: %w", path, err)
	}
	println(w, "-> shipper.toml: written")
	rep.record(outcomeWritten, "shipper.toml", path)
	return nil
}

// buildShipperToml renders a fresh shipper.toml. Schema mirrors
// specs/components/shipper.md §"Config file".
func buildShipperToml(configDir, dataDir string) string {
	operatorCreds := filepath.Join(configDir, "operator.creds")
	clickhousePass := filepath.Join(configDir, "clickhouse.password")
	bufferDir := filepath.Join(dataDir, "shipper-buffer")
	return fmt.Sprintf(`# sextant shipper.toml — written by `+"`sextant init`"+`.
# See specs/components/shipper.md §"Config file".

[nats]
# Leave url empty to read from runtime.json (recommended; the daemon
# picks an OS-allocated port on first boot).
url            = ""
operator_creds = %q

[clickhouse]
# Leave addr empty to read from runtime.json.
addr           = ""
database       = "sextant"
user           = "sextant"
password_file  = %q

[buffer]
dir            = %q
hard_cap_bytes = 10737418240  # 10 GiB

[batch]
max_events     = 1000
flush_interval = "100ms"
ack_wait       = "30s"

[shipper]
# "" = fail closed on hard cap (default). "drop_oldest" = drop oldest
# entries and emit audit.shipper_drop per drop.
degraded_mode    = ""
metrics_interval = "5s"
service_name     = "sextant-shipper"
host_id          = ""  # empty = os.Hostname()
`, operatorCreds, clickhousePass, bufferDir)
}

// stepDefaultTemplate writes templates/default.toml with the exact
// content from specs/architecture.md §11b ("Default template").
func stepDefaultTemplate(w io.Writer, rep *stepReport, templatesDir string, force, check bool) error {
	path := filepath.Join(templatesDir, "default.toml")
	if !force && fileExists(path) {
		if check {
			println(w, "-> template/default.toml: ok")
			rep.record(outcomeOK, "template/default.toml", path)
		} else {
			println(w, "-> template/default.toml: existing")
			rep.record(outcomeSatisfied, "template/default.toml", path)
		}
		return nil
	}
	if check {
		printf(w, "-> template/default.toml: would-write %s\n", path)
		rep.record(outcomeWouldWrite, "template/default.toml", path)
		return nil
	}
	if err := os.WriteFile(path, []byte(defaultTemplateBody), 0o600); err != nil {
		return fmt.Errorf("template/default.toml: write %s: %w", path, err)
	}
	println(w, "-> template/default.toml: written")
	rep.record(outcomeWritten, "template/default.toml", path)
	return nil
}

// defaultTemplateBody is the verbatim content from specs/architecture.md
// §11b "Default template". Keep them in sync.
const defaultTemplateBody = `# Default agent template — shipped with sextant init.
# See specs/architecture.md §11b for the schema.

name = "default"
description = "Minimal spawnable agent — assistant-style, broad reads, restricted writes."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "read.history", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
`

func fileExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st == nil {
		return false
	}
	return !st.IsDir()
}

func absPath(p string) (string, error) {
	expanded, err := expandHome(p)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p, err)
	}
	return abs, nil
}

func expandHome(p string) (string, error) {
	if len(p) < 2 || p[:2] != "~/" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, p[2:]), nil
}
