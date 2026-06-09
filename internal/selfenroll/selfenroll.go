// Package selfenroll is the one implementation of client self-enrollment
// (ADR-0020/0021): an identity-less local process mints an identity for itself
// over the bus's enrollment credential and records it as the active local
// context, so subsequent commands need no connection flags. Both faces share
// it — `sextant clients register --self` and the dash's zero-config first run
// (ADR-0024) — so the preflight, the mint, and the context write stay one
// implementation, never a copy.
package selfenroll

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
)

// enrollCredsPath is where the bus provisions the enrollment credential under
// its store dir. It mirrors pkg/bus.EnrollCredsPath — the write side — but is
// declared here so client-side callers (the dash) discover the file without
// linking the embedded bus server into their binary; the location is a store
// convention, like the bus.json discovery file.
func enrollCredsPath(store string) string { return filepath.Join(store, "enroll.creds") }

// ResolveBusURL picks the bus URL to record in a context: an explicit url, else
// the discovery file under store, else "" (the caller decides whether empty is
// fatal). Shared by `register --self`, `context add`, and the dash so they
// agree.
func ResolveBusURL(url, store string) string {
	if url != "" {
		return url
	}
	if info, err := conninfo.Read(filepath.Join(store, conninfo.DefaultFile)); err == nil {
		return info.URL
	}
	return ""
}

// Check pre-flights a self-enrollment BEFORE the bus mints anything, so a bad
// request can never strand a freshly minted identity. It rejects: an out path
// (self-enroll creds live in the context store, not at a path — a CLI-only
// flag interplay; non-CLI callers pass ""), a name clictx can't use as a handle
// (which would fail the post-mint write), and clobbering an existing context
// unless force is set.
func Check(name, out string, force bool) error {
	if out != "" {
		return fmt.Errorf("--out is not used with --self (self-enroll creds are saved in the context store)")
	}
	if err := clictx.ValidName(name); err != nil {
		return fmt.Errorf("invalid name %q for --self (override with --name): %w", name, err)
	}
	if !force {
		if _, err := clictx.Load(name); err == nil {
			return fmt.Errorf("context %q already exists (use --force to re-enroll, replacing it)", name)
		}
	}
	return nil
}

// Save records a just-self-enrolled identity as a local context: it writes the
// creds into the context store (0600), saves a context carrying the bus-minted
// identity, and makes it active. Self-enrollment is "I am now this identity,"
// so it always activates — `context use` switches away. Returns the creds path
// written.
func Save(name, kind, url string, issued sextant.IssuedClient) (string, error) {
	credsPath, err := clictx.WriteCreds(name, issued.Creds)
	if err != nil {
		return "", err
	}
	if err := clictx.Save(clictx.Context{
		Name: name, URL: url, ID: issued.ID, Display: name, Kind: kind, Creds: credsPath,
	}); err != nil {
		return "", err
	}
	if err := clictx.SetActive(name); err != nil {
		return "", err
	}
	return credsPath, nil
}

// SelfName resolves the default display name for a self-enrollment. It prefers
// an explicit env override, then the login name ($USER/$LOGNAME — the natural
// "who enrolled" on a real shell, and what a test harness can set per process),
// then the OS user, then the hostname.
func SelfName() string {
	for _, env := range []string{"SEXTANT_SELF_NAME", "USER", "LOGNAME"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "self"
}

// Result is what a completed self-enrollment produced: the bus-minted identity
// and the local context recording it.
type Result struct {
	// ID is the bus-minted client ULID.
	ID string
	// Name is the context handle (and display name) the identity was saved under.
	Name string
	// CredsPath is where the credential was written (inside the context store).
	CredsPath string
	// URL is the bus URL recorded in the context ("" when none was resolvable).
	URL string
}

// Enroll performs the full self-enrollment: preflight, connect with the bus's
// enrollment credential (under store), mint the identity, and save + activate
// the context. An empty name defaults via SelfName. ctx bounds the connect and
// the mint — pass a deadline-bound context so a wedged bus fails loud rather
// than hanging. url overrides the store's discovery file for both the dial and
// the recorded context URL.
func Enroll(ctx context.Context, name, kind, url, store string, force bool) (Result, error) {
	if name == "" {
		name = SelfName()
	}
	if err := Check(name, "", force); err != nil {
		return Result{}, err
	}
	iss, err := sextant.ConnectIssuer(ctx, sextant.Options{
		CredsPath:    enrollCredsPath(store),
		URL:          url,
		ConnInfoPath: filepath.Join(store, conninfo.DefaultFile),
	})
	if err != nil {
		return Result{}, fmt.Errorf("selfenroll: connect: %w", err)
	}
	defer iss.Close()
	issued, err := iss.Register(ctx, name, kind)
	if err != nil {
		return Result{}, fmt.Errorf("selfenroll: register: %w", err)
	}
	busURL := ResolveBusURL(url, store)
	credsPath, err := Save(name, kind, busURL, issued)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: issued.ID, Name: name, CredsPath: credsPath, URL: busURL}, nil
}
