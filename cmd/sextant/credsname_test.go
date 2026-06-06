package main

import "testing"

// TestSafeCredsName: the default creds filename must never be derived from a
// display name that could fail or escape the store. Path-bearing names fall back
// to the minted id; ordinary names are used as-is.
func TestSafeCredsName(t *testing.T) {
	cases := []struct{ name, id, want string }{
		{"alice", "01ID", "alice"},
		{"worker-7", "01ID", "worker-7"},
		{"a/b", "01ID", "01ID"},
		{"../x", "01ID", "01ID"},
		{"..", "01ID", "01ID"},
		{".", "01ID", "01ID"},
		{"", "01ID", "01ID"},
		{`a\b`, "01ID", "01ID"},
	}
	for _, c := range cases {
		if got := safeCredsName(c.name, c.id); got != c.want {
			t.Errorf("safeCredsName(%q, %q) = %q, want %q", c.name, c.id, got, c.want)
		}
	}
}
