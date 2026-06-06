package main

import (
	"fmt"
	"path/filepath"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
)

// resolveBusURL picks the bus URL to record in a context: an explicit url, else
// the discovery file under store, else "" (the caller decides whether empty is
// fatal). Shared by `register --self` and `context add` so they agree.
func resolveBusURL(url, store string) string {
	if url != "" {
		return url
	}
	if info, err := conninfo.Read(filepath.Join(store, conninfo.DefaultFile)); err == nil {
		return info.URL
	}
	return ""
}

// checkSelfEnroll pre-flights a `register --self` BEFORE the bus mints anything,
// so a bad request can never strand a freshly minted identity. It rejects: --out
// (self-enroll creds live in the context store, not at a path), a name clictx
// can't use as a handle (which would fail the post-mint write), and clobbering an
// existing context unless force is set.
func checkSelfEnroll(name, out string, force bool) error {
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

// saveSelfContext records a just-self-enrolled identity as a local context: it
// writes the creds into the context store (0600), saves a context carrying the
// bus-minted identity, and makes it active. Self-enrollment is "I am now this
// identity," so it always activates — `context use` switches away. Returns the
// creds path written.
func saveSelfContext(name, kind, url string, issued sextant.IssuedClient) (string, error) {
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
