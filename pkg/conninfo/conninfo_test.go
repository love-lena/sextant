package conninfo

import (
	"path/filepath"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFile)
	in := Info{URL: "nats://127.0.0.1:4222"}
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
