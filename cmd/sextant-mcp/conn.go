package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/pkg/conninfo"
)

// connFlags mirror the operator CLI's connection flags (cmd/sextant), so the
// MCP server is configured the same way every other client is.
type connFlags struct {
	creds   *string
	store   *string
	url     *string
	context *string
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		creds:   fs.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (or set $SEXTANT_CREDS)"),
		store:   fs.String("store", defaultStore(), "bus store directory for discovery (or set $SEXTANT_STORE)"),
		url:     fs.String("url", "", "bus URL (default: discovery file under --store)"),
		context: fs.String("context", os.Getenv("SEXTANT_CONTEXT"), "saved context to connect as (default: the active one)"),
	}
}

// defaultStore mirrors cmd/sextant's default exactly: $SEXTANT_STORE, then
// the user config dir, then a relative fallback.
func defaultStore() string {
	if v := os.Getenv("SEXTANT_STORE"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant", "jetstream")
	}
	return filepath.Join(".sextant", "jetstream")
}

func (cf connFlags) connInfoPath() string {
	return filepath.Join(*cf.store, conninfo.DefaultFile)
}
