package shipperboot

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAndFillRequiresPaths(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"missing-binary", Config{ConfigPath: "/c", RuntimePath: "/r"}, "BinaryPath"},
		{"missing-config", Config{BinaryPath: "/b", RuntimePath: "/r"}, "ConfigPath"},
		{"missing-runtime", Config{BinaryPath: "/b", ConfigPath: "/c"}, "RuntimePath"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.cfg.validateAndFill(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateAndFillDefaults(t *testing.T) {
	out, err := Config{BinaryPath: "/b", ConfigPath: "/c", RuntimePath: "/r"}.validateAndFill()
	if err != nil {
		t.Fatalf("validateAndFill: %v", err)
	}
	if out.StartupGrace != 2*time.Second {
		t.Errorf("StartupGrace default = %s, want 2s", out.StartupGrace)
	}
	if out.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout default = %s, want 10s", out.ShutdownTimeout)
	}
}

func TestDefaultConfigZeroPaths(t *testing.T) {
	c := DefaultConfig()
	if c.BinaryPath != "" || c.ConfigPath != "" || c.RuntimePath != "" {
		t.Errorf("DefaultConfig should leave required paths empty: %+v", c)
	}
	if c.StartupGrace == 0 || c.ShutdownTimeout == 0 {
		t.Errorf("DefaultConfig should set non-zero defaults: %+v", c)
	}
}
