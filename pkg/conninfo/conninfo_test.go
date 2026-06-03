package conninfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFile)
	in := Info{URL: "nats://127.0.0.1:4222", ClientUser: "client", ClientPassword: "deadbeef"}
	if err := Write(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestWriteMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFile)
	if err := Write(path, Info{URL: "x"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}
