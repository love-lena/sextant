// Package clictx is the client-side context store: saved (bus URL + identity +
// creds) profiles a person switches between, so the everyday commands need no
// connection flags. It is the kubectl/`nats context` pattern for sextant.
//
// A context is purely local. Its Name is a handle you choose (it defaults to the
// display name at register time) — it is NOT the identity. The identity is the
// bus-minted ULID in ID; the creds file at Creds carries it. Three distinct
// things meet here: the ULID (canonical, on the bus), the display name (a
// non-unique label on the bus record), and the context name (your machine's
// unique handle for "this identity on this bus").
//
// Layout under Root() ($SEXTANT_HOME, else <user-config>/sextant), kept separate
// from the bus --store (which is server state):
//
//	context/<name>.json   one context record (URL, ID, creds path, …)
//	creds/<name>.creds    the 0600 credential, referenced by path
//	active                the name of the active context
package clictx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned when a named context (or the file backing it) does not
// exist. Callers test it with errors.Is.
var ErrNotFound = errors.New("clictx: context not found")

// Context is a saved client identity and the bus it lives on. Name is the local
// handle (the file basename); it is not serialized into the file.
type Context struct {
	Name    string `json:"-"`
	URL     string `json:"url"`
	ID      string `json:"id"`
	Display string `json:"display,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Creds   string `json:"creds"`
}

// Root is the client-config root: $SEXTANT_HOME if set, else <user-config>/sextant.
func Root() string {
	if v := os.Getenv("SEXTANT_HOME"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant")
	}
	return filepath.Join(".sextant")
}

func contextDir() string { return filepath.Join(Root(), "context") }
func credsDir() string   { return filepath.Join(Root(), "creds") }
func activeFile() string { return filepath.Join(Root(), "active") }

// ValidName reports whether name is a usable context handle: non-empty and free
// of path separators or "."/".." (which would escape the context directory or
// fail as a filename). Callers can pre-flight a name before irreversible work —
// e.g. minting an identity — so a bad name fails before, not after.
func ValidName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("clictx: invalid context name %q", name)
	}
	return nil
}

func contextPath(name string) string { return filepath.Join(contextDir(), name+".json") }

// Save writes a context record. The Name is the file key, not stored in the file.
func Save(c Context) error {
	if err := ValidName(c.Name); err != nil {
		return err
	}
	if err := os.MkdirAll(contextDir(), 0o700); err != nil {
		return fmt.Errorf("clictx: create context dir: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("clictx: marshal: %w", err)
	}
	if err := os.WriteFile(contextPath(c.Name), b, 0o600); err != nil {
		return fmt.Errorf("clictx: write %s: %w", c.Name, err)
	}
	return nil
}

// Load reads the named context. It returns ErrNotFound if there is no such context.
func Load(name string) (Context, error) {
	var c Context
	if err := ValidName(name); err != nil {
		return c, err
	}
	b, err := os.ReadFile(contextPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	if err != nil {
		return c, fmt.Errorf("clictx: read %s: %w", name, err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("clictx: parse %s: %w", name, err)
	}
	c.Name = name
	return c, nil
}

// List returns every saved context, sorted by name. An absent store is empty.
func List() ([]Context, error) {
	entries, err := os.ReadDir(contextDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("clictx: read context dir: %w", err)
	}
	var out []Context
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		c, err := Load(name)
		if err != nil {
			continue // skip a corrupt entry rather than failing the whole list
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete removes the named context. If it was active, the active selection is
// cleared. Returns ErrNotFound if there is no such context.
func Delete(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	err := os.Remove(contextPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("clictx: delete %s: %w", name, err)
	}
	if Active() == name {
		_ = os.Remove(activeFile())
	}
	return nil
}

// Active is the name of the active context, or "" if none is set.
func Active() string {
	b, err := os.ReadFile(activeFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetActive marks name as the active context. It returns ErrNotFound if no such
// context exists.
func SetActive(name string) error {
	if _, err := Load(name); err != nil {
		return err
	}
	if err := os.MkdirAll(Root(), 0o700); err != nil {
		return fmt.Errorf("clictx: create root: %w", err)
	}
	if err := os.WriteFile(activeFile(), []byte(name+"\n"), 0o600); err != nil {
		return fmt.Errorf("clictx: write active: %w", err)
	}
	return nil
}

// WriteCreds writes a credential blob to creds/<name>.creds (0600) and returns
// the path. The secret material lives in its own private file, referenced by the
// context record rather than inlined into it.
func WriteCreds(name, creds string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	if err := os.MkdirAll(credsDir(), 0o700); err != nil {
		return "", fmt.Errorf("clictx: create creds dir: %w", err)
	}
	path := filepath.Join(credsDir(), name+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		return "", fmt.Errorf("clictx: write creds %s: %w", name, err)
	}
	return path, nil
}

// ErrNoIdentity is the "you didn't say who to connect as" error: no explicit
// creds, no named context, and no active context. The message doubles as the
// recovery recipe; callers (the CLI, sextant-mcp) surface it verbatim.
var ErrNoIdentity = errors.New("no credentials: pass --creds, set $SEXTANT_CREDS, or select a context with `sextant context use <name>` (create one with `sextant context add`)")

// ResolvedConn is what Resolve picked: the creds path and bus URL to connect
// with, and Context, the context name that supplied them ("" when explicit
// creds won — then nothing was read from the store).
type ResolvedConn struct {
	Creds   string
	URL     string
	Context string
}

// Resolve picks the credentials and bus URL for a connection. Precedence:
// explicit creds win (URL then comes from url or store discovery); otherwise a
// context — contextName if non-empty, else the active one — supplies both creds
// and URL. An explicit url still overrides a context's URL.
func Resolve(creds, url, contextName string) (ResolvedConn, error) {
	if creds != "" {
		return ResolvedConn{Creds: creds, URL: url}, nil
	}
	name := contextName
	if name == "" {
		name = Active()
	}
	if name == "" {
		return ResolvedConn{}, ErrNoIdentity
	}
	c, err := Load(name)
	if err != nil {
		return ResolvedConn{}, fmt.Errorf("context %q: %w", name, err)
	}
	if url == "" {
		url = c.URL
	}
	return ResolvedConn{Creds: c.Creds, URL: url, Context: name}, nil
}
