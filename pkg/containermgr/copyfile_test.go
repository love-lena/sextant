package containermgr

import (
	"archive/tar"
	"bytes"
	"errors"
	"testing"
)

// tarOf builds a single-entry tar of name→body for the extraction tests.
func tarOf(t *testing.T, entries []tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i, h := range entries {
		hdr := h
		hdr.Size = int64(len(bodies[i]))
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write(bodies[i]); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

// TestFirstRegularFileFromTar_SingleFile mirrors Docker's
// CopyFromContainer of a single file: a one-entry tar whose body is the
// file's bytes. This is the snapshot-on-stop extraction path.
func TestFirstRegularFileFromTar_SingleFile(t *testing.T) {
	t.Parallel()
	want := []byte(`{"type":"assistant","sessionId":"s1"}` + "\n")
	raw := tarOf(
		t,
		[]tar.Header{{Name: "s1.jsonl", Typeflag: tar.TypeReg, Mode: 0o600}},
		[][]byte{want},
	)
	got, err := firstRegularFileFromTar(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("firstRegularFileFromTar: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFirstRegularFileFromTar_SkipsDirEntry — a tar that leads with a
// directory entry (the per-cwd dir) must skip it and return the first
// regular file.
func TestFirstRegularFileFromTar_SkipsDirEntry(t *testing.T) {
	t.Parallel()
	want := []byte("line-1\nline-2\n")
	raw := tarOf(
		t,
		[]tar.Header{
			{Name: "-workspace/", Typeflag: tar.TypeDir, Mode: 0o700},
			{Name: "-workspace/s1.jsonl", Typeflag: tar.TypeReg, Mode: 0o600},
		},
		[][]byte{{}, want},
	)
	got, err := firstRegularFileFromTar(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("firstRegularFileFromTar: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFirstRegularFileFromTar_NoRegularFile — an archive with only a
// directory surfaces errTarNoRegularFile so CopyFileFromContainer can map
// it to ErrPathNotFound (a soft skip on the snapshot path).
func TestFirstRegularFileFromTar_NoRegularFile(t *testing.T) {
	t.Parallel()
	raw := tarOf(
		t,
		[]tar.Header{{Name: "empty/", Typeflag: tar.TypeDir, Mode: 0o700}},
		[][]byte{{}},
	)
	_, err := firstRegularFileFromTar(bytes.NewReader(raw))
	if !errors.Is(err, errTarNoRegularFile) {
		t.Errorf("err = %v, want errTarNoRegularFile", err)
	}
}
