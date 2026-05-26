package client_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/client"
)

func TestLoadConfigParsesAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.toml")
	body := `
[nats]
url = "nats://127.0.0.1:4222"

[operator]
user = "operator"
password = "secret"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := client.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NATS.URL != "nats://127.0.0.1:4222" {
		t.Fatalf("NATS.URL = %q", cfg.NATS.URL)
	}
	if cfg.Operator.User != "operator" || cfg.Operator.Password != "secret" {
		t.Fatalf("operator = %+v", cfg.Operator)
	}
	if cfg.Client.ConnectTimeout.AsDuration() != 10*time.Second {
		t.Fatalf("ConnectTimeout default = %s", cfg.Client.ConnectTimeout.AsDuration())
	}
	if cfg.Client.RequestTimeout.AsDuration() != 30*time.Second {
		t.Fatalf("RequestTimeout default = %s", cfg.Client.RequestTimeout.AsDuration())
	}
	if cfg.Client.LogLevel != "info" {
		t.Fatalf("LogLevel default = %q", cfg.Client.LogLevel)
	}
}

func TestLoadConfigRejectsBothPasswordAndCreds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.toml")
	body := `
[nats]
url = "nats://127.0.0.1:4222"

[operator]
user = "operator"
password = "secret"
creds_path = "~/.config/sextant/operator.creds"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := client.LoadConfig(path); err == nil {
		t.Fatal("LoadConfig must reject both password and creds_path")
	} else if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want 'mutually exclusive'", err)
	}
}

func TestLoadConfigRequiresOneCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.toml")
	body := `
[nats]
url = "nats://127.0.0.1:4222"

[operator]
user = "operator"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := client.LoadConfig(path); err == nil {
		t.Fatal("LoadConfig must reject missing credential")
	}
}

func TestLoadConfigRequiresNATSURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.toml")
	body := `
[operator]
user = "operator"
password = "secret"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := client.LoadConfig(path); err == nil {
		t.Fatal("LoadConfig must reject missing nats.url")
	}
}

func TestLoadConfigParsesDurationsAndLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.toml")
	body := `
[nats]
url = "nats://127.0.0.1:4222"

[operator]
user = "operator"
password = "secret"

[client]
connect_timeout = "2s"
request_timeout = "1m"
log_level = "debug"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := client.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Client.ConnectTimeout.AsDuration() != 2*time.Second {
		t.Fatalf("ConnectTimeout = %s", cfg.Client.ConnectTimeout.AsDuration())
	}
	if cfg.Client.RequestTimeout.AsDuration() != time.Minute {
		t.Fatalf("RequestTimeout = %s", cfg.Client.RequestTimeout.AsDuration())
	}
	if cfg.Client.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", cfg.Client.LogLevel)
	}
}

func TestLoadConfigRejectsInvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.toml")
	body := `
[nats]
url = "nats://127.0.0.1:4222"

[operator]
user = "operator"
password = "secret"

[client]
log_level = "loud"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := client.LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig must reject invalid log_level")
	}
}

func TestLoadConfigMissingFileSurfacesError(t *testing.T) {
	_, err := client.LoadConfig(filepath.Join(t.TempDir(), "no-such-file.toml"))
	if err == nil {
		t.Fatal("LoadConfig must error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error chain should include os.ErrNotExist; got %v", err)
	}
}
