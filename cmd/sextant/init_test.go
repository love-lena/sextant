package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/shipper"
)

func tempInitOpts(t *testing.T) initOptions {
	t.Helper()
	dir := t.TempDir()
	return initOptions{
		ConfigDir: filepath.Join(dir, "cfg"),
		DataDir:   filepath.Join(dir, "data"),
	}
}

func TestInitCreatesEveryArtifact(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}

	checks := []struct {
		path string
		mode os.FileMode
		isFn func(os.FileInfo) bool
	}{
		{opts.ConfigDir, 0o700, isDir},
		{filepath.Join(opts.ConfigDir, "ca.key"), 0o600, isFile},
		{filepath.Join(opts.ConfigDir, "ca.pub"), 0o644, isFile},
		{filepath.Join(opts.ConfigDir, "sextantd.toml"), 0o600, isFile},
		{filepath.Join(opts.ConfigDir, "client.toml"), 0o600, isFile},
		{filepath.Join(opts.ConfigDir, "shipper.toml"), 0o600, isFile},
		{filepath.Join(opts.ConfigDir, "operator.creds"), 0o600, isFile},
		{filepath.Join(opts.ConfigDir, "clickhouse.password"), 0o600, isFile},
		{filepath.Join(opts.ConfigDir, "templates"), 0o700, isDir},
		{filepath.Join(opts.ConfigDir, "templates", "default.toml"), 0o600, isFile},
		{opts.DataDir, 0o750, isDir},
		{filepath.Join(opts.DataDir, "nats"), 0o750, isDir},
		{filepath.Join(opts.DataDir, "clickhouse"), 0o750, isDir},
		{filepath.Join(opts.DataDir, "shipper-buffer"), 0o750, isDir},
		{filepath.Join(opts.DataDir, "test"), 0o750, isDir},
	}
	for _, c := range checks {
		st, err := os.Stat(c.path)
		if err != nil {
			t.Errorf("stat %s: %v", c.path, err)
			continue
		}
		if !c.isFn(st) {
			t.Errorf("%s: wrong kind", c.path)
			continue
		}
		if st.Mode().Perm() != c.mode {
			t.Errorf("%s: mode %o, want %o", c.path, st.Mode().Perm(), c.mode)
		}
	}

	// CA must validate.
	if _, err := authjwt.LoadCA(
		filepath.Join(opts.ConfigDir, "ca.key"),
		filepath.Join(opts.ConfigDir, "ca.pub"),
	); err != nil {
		t.Errorf("CA didn't validate: %v", err)
	}
	// sextantd.toml must load.
	if _, err := sextantd.LoadConfig(filepath.Join(opts.ConfigDir, "sextantd.toml")); err != nil {
		t.Errorf("sextantd.toml load: %v", err)
	}
	// operator.creds must load and have a non-empty password.
	creds, err := sextantd.ReadOperatorCreds(filepath.Join(opts.ConfigDir, "operator.creds"))
	if err != nil {
		t.Errorf("operator.creds: %v", err)
	}
	if creds.User != "operator" || len(creds.Password) < 32 {
		t.Errorf("operator.creds wrong: %+v", creds)
	}
	// shipper.toml must load and resolve cleanly.
	shipperCfg, err := shipper.LoadConfig(filepath.Join(opts.ConfigDir, "shipper.toml"))
	if err != nil {
		t.Errorf("shipper.toml load: %v", err)
	}
	if shipperCfg.Buffer.HardCapBytes != shipper.DefaultHardCapBytes {
		t.Errorf("shipper.toml: HardCapBytes = %d, want default %d", shipperCfg.Buffer.HardCapBytes, shipper.DefaultHardCapBytes)
	}
	if shipperCfg.NATS.OperatorCreds == "" {
		t.Errorf("shipper.toml: operator_creds is empty")
	}

	// Default template carries the spec-mandated permission_ceiling line.
	body, err := os.ReadFile(filepath.Join(opts.ConfigDir, "templates", "default.toml"))
	if err != nil {
		t.Fatalf("read default template: %v", err)
	}
	if !bytes.Contains(body, []byte(`permission_ceiling = "auto"`)) {
		t.Errorf("default template missing permission_ceiling")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	opts := tempInitOpts(t)
	var buf1 bytes.Buffer
	if err := doInit(context.Background(), &buf1, opts); err != nil {
		t.Fatalf("first doInit: %v", err)
	}
	caKeyBefore, err := os.ReadFile(filepath.Join(opts.ConfigDir, "ca.key"))
	if err != nil {
		t.Fatalf("read ca.key: %v", err)
	}

	var buf2 bytes.Buffer
	if err := doInit(context.Background(), &buf2, opts); err != nil {
		t.Fatalf("second doInit: %v", err)
	}
	caKeyAfter, err := os.ReadFile(filepath.Join(opts.ConfigDir, "ca.key"))
	if err != nil {
		t.Fatalf("read ca.key second time: %v", err)
	}
	if !bytes.Equal(caKeyBefore, caKeyAfter) {
		t.Errorf("ca.key changed across idempotent re-runs")
	}
	// Output should be all "existing" the second time.
	if !bytes.Contains(buf2.Bytes(), []byte("ca: existing")) {
		t.Errorf("second run did not detect existing CA: %s", buf2.String())
	}
}

func TestInitForceRegeneratesCA(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("first doInit: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(opts.ConfigDir, "ca.key"))
	if err != nil {
		t.Fatalf("read ca.key: %v", err)
	}

	opts.Force = true
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("forced doInit: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(opts.ConfigDir, "ca.key"))
	if err != nil {
		t.Fatalf("read ca.key after force: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Errorf("--force did not regenerate ca.key")
	}
}

func TestInitRejectsHalfInstalledCA(t *testing.T) {
	opts := tempInitOpts(t)
	if err := os.MkdirAll(opts.ConfigDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Plant a key without a pub.
	if err := os.WriteFile(filepath.Join(opts.ConfigDir, "ca.key"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("plant ca.key: %v", err)
	}
	var buf bytes.Buffer
	err := doInit(context.Background(), &buf, opts)
	if err == nil {
		t.Fatal("expected doInit to reject half-installed CA")
	}
}

func isDir(st os.FileInfo) bool  { return st.IsDir() }
func isFile(st os.FileInfo) bool { return !st.IsDir() }

// TestInit_SummaryLine_FreshInstall asserts that running `sextant init` on a
// fresh (empty) install ends with a one-line summary stating how many steps
// were written. Operators rely on this line to know whether anything
// changed.
//
// Issue: feat-daemon-lifecycle-ergonomics (#1 — init clarity)
func TestInit_SummaryLine_FreshInstall(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "init: ") {
		t.Fatalf("expected summary line prefixed with 'init: ', got:\n%s", out)
	}
	if !strings.Contains(out, "written") {
		t.Errorf("fresh install: expected summary to contain 'written', got:\n%s", out)
	}
	// On a fresh install nothing pre-existed, so "already satisfied" must
	// not appear in the summary line.
	summary := lastSummaryLine(t, out)
	if strings.Contains(summary, "already satisfied") {
		t.Errorf("fresh install: summary should not mention 'already satisfied', got: %q", summary)
	}
}

// TestInit_SummaryLine_AllSatisfied asserts that re-running `sextant init`
// after a complete install reports every step as already satisfied. This is
// the bug the feature targets: operators rerun init and have no way to tell
// it was a no-op.
func TestInit_SummaryLine_AllSatisfied(t *testing.T) {
	opts := tempInitOpts(t)
	var buf1 bytes.Buffer
	if err := doInit(context.Background(), &buf1, opts); err != nil {
		t.Fatalf("first doInit: %v", err)
	}
	var buf2 bytes.Buffer
	if err := doInit(context.Background(), &buf2, opts); err != nil {
		t.Fatalf("second doInit: %v", err)
	}
	out := buf2.String()
	summary := lastSummaryLine(t, out)
	if !strings.Contains(summary, "already satisfied") {
		t.Errorf("second run: expected summary to mention 'already satisfied', got: %q\nfull output:\n%s", summary, out)
	}
	if !strings.Contains(summary, "0 written") {
		t.Errorf("second run: expected '0 written' in summary, got: %q", summary)
	}
}

// TestInit_Check_AllPresent_Exit0 verifies that `--check` against a complete
// install returns no error (exit 0) and does not modify any file on disk.
// File mtimes are recorded before and after; any change indicates a write.
func TestInit_Check_AllPresent_Exit0(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("seed doInit: %v", err)
	}

	mtimes := snapshotMTimes(t, opts.ConfigDir)
	dataMtimes := snapshotMTimes(t, opts.DataDir)

	checkOpts := opts
	checkOpts.Check = true
	var checkBuf bytes.Buffer
	if err := doInit(context.Background(), &checkBuf, checkOpts); err != nil {
		t.Fatalf("doInit --check on complete install returned error: %v\noutput:\n%s", err, checkBuf.String())
	}

	// Nothing should have been written.
	assertMTimesUnchanged(t, opts.ConfigDir, mtimes)
	assertMTimesUnchanged(t, opts.DataDir, dataMtimes)

	out := checkBuf.String()
	if !strings.Contains(out, "check: ok") {
		t.Errorf("expected check output to contain 'check: ok', got:\n%s", out)
	}
}

// TestInit_Check_Missing_Exit2 verifies that `--check` against an incomplete
// install returns an error mapped to exit 2 and names the missing files in
// the output so the operator can act.
func TestInit_Check_Missing_Exit2(t *testing.T) {
	opts := tempInitOpts(t)
	checkOpts := opts
	checkOpts.Check = true
	var buf bytes.Buffer
	err := doInit(context.Background(), &buf, checkOpts)
	if err == nil {
		t.Fatalf("expected --check on empty dir to return an error, got nil; output:\n%s", buf.String())
	}
	if exitCodeFor(err) != exitSystem {
		t.Errorf("expected exit code %d (system), got %d; err=%v", exitSystem, exitCodeFor(err), err)
	}
	out := buf.String()
	// At minimum, the CA and one config file should be reported missing.
	for _, needle := range []string{"ca", "sextantd.toml", "would"} {
		if !strings.Contains(out, needle) {
			t.Errorf("expected --check output to mention %q, got:\n%s", needle, out)
		}
	}
	// No files should have been written.
	if _, err := os.Stat(filepath.Join(opts.ConfigDir, "ca.key")); !os.IsNotExist(err) {
		t.Errorf("--check should not have created ca.key (err=%v)", err)
	}
}

// snapshotMTimes walks dir and returns path -> modtime.
func snapshotMTimes(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out[path] = info.ModTime().UnixNano()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func assertMTimesUnchanged(t *testing.T, dir string, before map[string]int64) {
	t.Helper()
	after := snapshotMTimes(t, dir)
	for path, ts := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("%s: file disappeared during --check", path)
			continue
		}
		if got != ts {
			t.Errorf("%s: mtime changed during --check (before=%d after=%d)", path, ts, got)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s: file appeared during --check", path)
		}
	}
}

// lastSummaryLine returns the last non-empty line of the init output that
// starts with the summary prefix.
func lastSummaryLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, "init: ") {
			return line
		}
	}
	t.Fatalf("no summary line ('init: ...') in output:\n%s", out)
	return ""
}

// TestInitOutputMentionsMakeInstallOnMacOS asserts that `sextant init` ends with
// a note steering operators to `make install`. Plain `cp bin/* ~/.local/bin/`
// stamps com.apple.provenance onto the destination, and Gatekeeper SIGKILLs
// the resulting binary on invocation (exit 137, no stderr). The note only
// appears on darwin; Linux installs are unaffected.
//
// Issue: docs-install-via-make-install-not-cp
func TestInitOutputMentionsMakeInstallOnMacOS(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}

	out := buf.String()
	const needle = "make install"
	const gatekeeper = "Gatekeeper"

	if runtime.GOOS == "darwin" {
		if !strings.Contains(out, needle) {
			t.Errorf("darwin: expected init output to mention %q, got:\n%s", needle, out)
		}
		if !strings.Contains(out, gatekeeper) {
			t.Errorf("darwin: expected init output to mention %q, got:\n%s", gatekeeper, out)
		}
		return
	}

	// Non-darwin: the note is irrelevant and should NOT appear.
	if strings.Contains(out, needle) {
		t.Errorf("%s: did not expect %q in init output, got:\n%s", runtime.GOOS, needle, out)
	}
	if strings.Contains(out, gatekeeper) {
		t.Errorf("%s: did not expect %q in init output, got:\n%s", runtime.GOOS, gatekeeper, out)
	}
}
