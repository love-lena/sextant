package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
)

// runInit implements `sextant init`. Idempotent — each step inspects the
// filesystem first and skips when state is already correct. `--force`
// re-generates every file (CA included, which DESTROYS already-issued
// JWTs; the prompt is on the operator).
func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	force := fs.Bool("force", false, "regenerate every file even if present")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(initUsage)
		return nil
	}

	cfgPath, dataPath, err := resolveInitPaths(*configDir, *dataDir)
	if err != nil {
		return err
	}
	return doInit(ctx, os.Stdout, initOptions{
		ConfigDir: cfgPath,
		DataDir:   dataPath,
		Force:     *force,
	})
}

const initUsage = `usage: sextant init [--config-dir PATH] [--data-dir PATH] [--force]

Creates the config and data directories, generates the signing CA, writes
sextantd.toml + client.toml + operator.creds, and seeds default templates.

Re-running is idempotent: every step skips when state is already correct.
--force regenerates every file, including the CA (which invalidates any
JWTs already issued).`

type initOptions struct {
	ConfigDir string
	DataDir   string
	Force     bool
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

// doInit runs the init steps in order, writing progress to w. Each step
// reports one of: "generated", "existing", "regenerated".
func doInit(_ context.Context, w io.Writer, opts initOptions) error {
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)

	// 1. Config dir (0700).
	if err := ensureDir(w, "config-dir", opts.ConfigDir, 0o700); err != nil {
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
		if err := ensureDir(w, "data-dir", d, 0o750); err != nil {
			return err
		}
	}

	// 3. CA keypair.
	if err := stepCA(w, cfg.CA.KeyPath, cfg.CA.PubPath, opts.Force); err != nil {
		return err
	}

	// 4. Operator NATS creds.
	if err := stepOperatorCreds(w, cfg.NATS.OperatorCreds, opts.Force); err != nil {
		return err
	}

	// 5. ClickHouse password file.
	if err := stepClickHousePassword(w, cfg.ClickHouse.PasswordFile, opts.Force); err != nil {
		return err
	}

	// 6. sextantd.toml.
	sextantdTomlPath := filepath.Join(opts.ConfigDir, "sextantd.toml")
	if err := stepSextantdConfig(w, sextantdTomlPath, cfg, opts.Force); err != nil {
		return err
	}

	// 7. client.toml.
	if err := stepClientConfig(w, cfg.Paths.ClientConfig, cfg.NATS.OperatorCreds, opts.Force); err != nil {
		return err
	}

	// 8. Templates dir + default.toml.
	if err := ensureDir(w, "templates-dir", cfg.Paths.TemplatesDir, 0o700); err != nil {
		return err
	}
	if err := stepDefaultTemplate(w, cfg.Paths.TemplatesDir, opts.Force); err != nil {
		return err
	}

	println(w, "done.")
	return nil
}

func ensureDir(w io.Writer, label, path string, mode os.FileMode) error {
	st, err := os.Stat(path)
	switch {
	case err == nil:
		if !st.IsDir() {
			return fmt.Errorf("%s exists and is not a directory: %s", label, path)
		}
		// Best-effort: align mode if it drifted.
		if st.Mode().Perm() != mode {
			if err := os.Chmod(path, mode); err != nil {
				return fmt.Errorf("%s chmod %s: %w", label, path, err)
			}
		}
		printf(w, "-> %s: existing %s\n", label, path)
		return nil
	case os.IsNotExist(err):
		if err := os.MkdirAll(path, mode); err != nil {
			return fmt.Errorf("%s mkdir %s: %w", label, path, err)
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("%s chmod %s: %w", label, path, err)
		}
		printf(w, "-> %s: created %s\n", label, path)
		return nil
	default:
		return fmt.Errorf("%s stat %s: %w", label, path, err)
	}
}

func stepCA(w io.Writer, keyPath, pubPath string, force bool) error {
	keyExists := fileExists(keyPath)
	pubExists := fileExists(pubPath)

	switch {
	case keyExists && pubExists && !force:
		// Sanity: parse the existing pair so we don't silently sit on a
		// corrupted CA.
		if _, err := authjwt.LoadCA(keyPath, pubPath); err != nil {
			return fmt.Errorf("ca: existing CA failed validation: %w (re-run with --force to regenerate)", err)
		}
		println(w, "-> ca: existing")
		return nil
	case keyExists != pubExists && !force:
		return fmt.Errorf("ca: half-installed (key=%v pub=%v); re-run with --force", keyExists, pubExists)
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
	return nil
}

func stepOperatorCreds(w io.Writer, path string, force bool) error {
	if !force && fileExists(path) {
		if _, err := sextantd.ReadOperatorCreds(path); err != nil {
			return fmt.Errorf("operator-creds: existing file failed validation: %w (re-run with --force)", err)
		}
		println(w, "-> operator-creds: existing")
		return nil
	}
	pw, err := sextantd.GenerateOperatorPassword()
	if err != nil {
		return fmt.Errorf("operator-creds: %w", err)
	}
	if err := sextantd.WriteOperatorCreds(path, sextantd.OperatorCreds{User: "operator", Password: pw}); err != nil {
		return err
	}
	verb := "generated"
	if fileExists(path) && force {
		verb = "regenerated"
	}
	printf(w, "-> operator-creds: %s\n", verb)
	return nil
}

func stepClickHousePassword(w io.Writer, path string, force bool) error {
	if !force && fileExists(path) {
		if _, err := sextantd.ReadPasswordFile(path); err != nil {
			return fmt.Errorf("clickhouse-password: existing failed validation: %w", err)
		}
		println(w, "-> clickhouse-password: existing")
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
	return nil
}

func stepSextantdConfig(w io.Writer, path string, cfg sextantd.Config, force bool) error {
	if !force && fileExists(path) {
		// Parse it to ensure it loads cleanly.
		if _, err := sextantd.LoadConfig(path); err != nil {
			return fmt.Errorf("sextantd.toml: existing file failed to parse: %w (re-run with --force)", err)
		}
		println(w, "-> sextantd.toml: existing")
		return nil
	}
	if err := sextantd.SaveConfig(path, cfg); err != nil {
		return err
	}
	println(w, "-> sextantd.toml: written")
	return nil
}

func stepClientConfig(w io.Writer, path, credsPath string, force bool) error {
	if !force && fileExists(path) {
		println(w, "-> client.toml: existing")
		return nil
	}
	body := buildClientToml(credsPath)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("client.toml: write %s: %w", path, err)
	}
	println(w, "-> client.toml: written")
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

// stepDefaultTemplate writes templates/default.toml with the exact
// content from specs/architecture.md §11b ("Default template").
func stepDefaultTemplate(w io.Writer, templatesDir string, force bool) error {
	path := filepath.Join(templatesDir, "default.toml")
	if !force && fileExists(path) {
		println(w, "-> template/default.toml: existing")
		return nil
	}
	if err := os.WriteFile(path, []byte(defaultTemplateBody), 0o600); err != nil {
		return fmt.Errorf("template/default.toml: write %s: %w", path, err)
	}
	println(w, "-> template/default.toml: written")
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
