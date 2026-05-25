package main

import (
	"errors"
	"reflect"
	"testing"
)

// TestSplitDoubleDashPartitions verifies the `--` split that powers
// `sextant exec <agent> -- <cmd> args`.
func TestSplitDoubleDashPartitions(t *testing.T) {
	cases := []struct {
		name       string
		in         []string
		wantBefore []string
		wantAfter  []string
	}{
		{
			name:       "with separator",
			in:         []string{"agent-uuid", "--workdir", "/tmp", "--", "ls", "-lah"},
			wantBefore: []string{"agent-uuid", "--workdir", "/tmp"},
			wantAfter:  []string{"ls", "-lah"},
		},
		{
			name:       "no separator returns input unchanged",
			in:         []string{"agent-uuid", "--workdir", "/tmp"},
			wantBefore: []string{"agent-uuid", "--workdir", "/tmp"},
			wantAfter:  nil,
		},
		{
			name:       "empty after",
			in:         []string{"agent-uuid", "--"},
			wantBefore: []string{"agent-uuid"},
			wantAfter:  []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, after := splitDoubleDash(tc.in)
			if !reflect.DeepEqual(before, tc.wantBefore) {
				t.Errorf("before = %v, want %v", before, tc.wantBefore)
			}
			if !reflect.DeepEqual(after, tc.wantAfter) {
				t.Errorf("after = %v, want %v", after, tc.wantAfter)
			}
		})
	}
}

// TestEnvFlagAcceptsKV parses several --env K=V pairs and asserts the
// flag's pairs map captured each one. Also pins the "values may
// contain '='" semantic so users can pass tokens like SECRET=foo=bar.
func TestEnvFlagAcceptsKV(t *testing.T) {
	e := &envFlag{}
	if err := e.Set("FOO=1"); err != nil {
		t.Fatalf("Set FOO=1: %v", err)
	}
	if err := e.Set("BAR=two=parts"); err != nil {
		t.Fatalf("Set BAR=two=parts: %v", err)
	}
	if e.pairs["FOO"] != "1" {
		t.Errorf("FOO = %q", e.pairs["FOO"])
	}
	if e.pairs["BAR"] != "two=parts" {
		t.Errorf("BAR = %q", e.pairs["BAR"])
	}
}

func TestEnvFlagRejectsBareKey(t *testing.T) {
	e := &envFlag{}
	if err := e.Set("NOEQUALS"); err == nil {
		t.Error("Set('NOEQUALS') must error")
	}
}

func TestExitCodeErrorIsTypedSentinel(t *testing.T) {
	err := &exitCodeError{code: 42}
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatal("errors.As must match exitCodeError")
	}
	if ec.code != 42 {
		t.Errorf("code = %d, want 42", ec.code)
	}
	if exitCodeFor(err) != 42 {
		t.Errorf("exitCodeFor returned %d, want 42", exitCodeFor(err))
	}
}
