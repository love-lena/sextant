package sextantd

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// OperatorCreds is the TOML shape of ~/.config/sextant/operator.creds.
// See specs/components/sextantd.md §"operator.creds format".
type OperatorCreds struct {
	User     string `toml:"user"`
	Password string `toml:"password"`
}

// GenerateOperatorPassword returns 32 cryptographically random bytes
// encoded as base64url with no padding. The wire-shape is what NATS
// expects for the operator user's password.
func GenerateOperatorPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sextantd: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// WriteOperatorCreds serializes c to path with mode 0600.
func WriteOperatorCreds(path string, c OperatorCreds) error {
	if c.User == "" {
		return fmt.Errorf("sextantd: operator creds user must be set")
	}
	if c.Password == "" {
		return fmt.Errorf("sextantd: operator creds password must be set")
	}
	raw, err := toml.Marshal(c) //nolint:gosec // serializing the secret to its dedicated 0600 file is the intended use
	if err != nil {
		return fmt.Errorf("sextantd: marshal operator creds: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("sextantd: write %s: %w", path, err)
	}
	return nil
}

// ReadOperatorCreds parses path into an OperatorCreds.
func ReadOperatorCreds(path string) (OperatorCreds, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled
	if err != nil {
		return OperatorCreds{}, fmt.Errorf("sextantd: read %s: %w", path, err)
	}
	var c OperatorCreds
	if err := toml.Unmarshal(raw, &c); err != nil {
		return OperatorCreds{}, fmt.Errorf("sextantd: parse %s: %w", path, err)
	}
	c.User = strings.TrimSpace(c.User)
	c.Password = strings.TrimSpace(c.Password)
	if c.User == "" || c.Password == "" {
		return OperatorCreds{}, fmt.Errorf("sextantd: %s missing user or password", path)
	}
	return c, nil
}

// WritePasswordFile writes a single-line password file at path with
// mode 0600. Used for the ClickHouse password file (specs say plain
// text, mode-0600, no TOML wrapper).
func WritePasswordFile(path, password string) error {
	if password == "" {
		return fmt.Errorf("sextantd: empty password")
	}
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		return fmt.Errorf("sextantd: write %s: %w", path, err)
	}
	return nil
}

// ReadPasswordFile reads a single-line password file from path.
func ReadPasswordFile(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled
	if err != nil {
		return "", fmt.Errorf("sextantd: read %s: %w", path, err)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "", fmt.Errorf("sextantd: %s is empty", path)
	}
	return s, nil
}
