package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
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
