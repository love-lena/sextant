package conformance

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// VectorsDir is the conventional root of the vector tree, relative to this
// package. The Go runner and any other-language suite read from the same place.
//
//	protocol/conformance/vectors/wire/*.json           — wire (frame-codec) vectors
//	protocol/conformance/vectors/<convention>/*.json   — op-transcript vectors
const VectorsDir = "vectors"

// LoadedOpTranscript pairs a parsed vector with the path it came from, so a
// runner can name the file in a failure. Sorted by path for deterministic runs.
type LoadedOpTranscript struct {
	Path   string
	Vector OpTranscriptVector
}

// LoadOpTranscripts discovers and parses every op-transcript vector under
// dir/<convention>, for the named convention (e.g. "goals"). It returns them
// sorted by path. dir is the vectors root (VectorsDir resolved to an absolute
// or test-relative path). A convention with no directory yet (the common case
// before TASK-173 lands conv/goals) returns an empty slice, not an error — a
// suite with no vectors to replay is valid, not a failure.
func LoadOpTranscripts(dir, convention string) ([]LoadedOpTranscript, error) {
	convDir := filepath.Join(dir, convention)
	entries, err := os.ReadDir(convDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("conformance: read %s: %w", convDir, err)
	}
	var out []LoadedOpTranscript
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(convDir, e.Name())
		v, err := loadOpTranscript(path)
		if err != nil {
			return nil, err
		}
		out = append(out, LoadedOpTranscript{Path: path, Vector: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func loadOpTranscript(path string) (OpTranscriptVector, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OpTranscriptVector{}, fmt.Errorf("conformance: read %s: %w", path, err)
	}
	var v OpTranscriptVector
	if err := json.Unmarshal(data, &v); err != nil {
		return OpTranscriptVector{}, fmt.Errorf("conformance: parse %s: %w", path, err)
	}
	if err := v.Validate(); err != nil {
		return OpTranscriptVector{}, fmt.Errorf("conformance: %s: %w", path, err)
	}
	return v, nil
}

// AllConventions lists the convention subdirectories under dir (every directory
// except "wire"). It lets a suite discover what conventions have vectors without
// hard-coding the list — a new convention's vectors are picked up by dropping a
// directory in. Returns an empty slice if dir does not exist yet.
func AllConventions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("conformance: read %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == string(KindWire) {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// LoadedWire pairs a parsed wire vector with its path.
type LoadedWire struct {
	Path   string
	Vector WireVector
}

// LoadWireVectors discovers and parses every wire (frame-codec) vector under
// dir/wire. Like LoadOpTranscripts it returns an empty slice when the directory
// is absent, and is sorted by path.
func LoadWireVectors(dir string) ([]LoadedWire, error) {
	wireDir := filepath.Join(dir, string(KindWire))
	entries, err := os.ReadDir(wireDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("conformance: read %s: %w", wireDir, err)
	}
	var out []LoadedWire
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(wireDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("conformance: read %s: %w", path, err)
		}
		var v WireVector
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("conformance: parse %s: %w", path, err)
		}
		out = append(out, LoadedWire{Path: path, Vector: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// MethodsOps reads the operation names declared in a methods.json document.
// It is the language-neutral half of the protocol-surface parity check: a
// runner asserts every op used across vectors ∈ this set. It takes the raw
// methods.json bytes (the caller resolves the path) so this package needs no
// knowledge of the repo layout.
func MethodsOps(methodsJSON []byte) (map[string]bool, error) {
	var doc struct {
		Operations []struct {
			Name string `json:"name"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(methodsJSON, &doc); err != nil {
		return nil, fmt.Errorf("conformance: parse methods.json: %w", err)
	}
	ops := make(map[string]bool, len(doc.Operations))
	for _, op := range doc.Operations {
		ops[op.Name] = true
	}
	return ops, nil
}

// WalkVectorFiles returns every *.json vector file under dir (both wire and
// op-transcript), sorted. It is a convenience for a suite that wants to assert
// something across all vectors regardless of kind (e.g. epoch parity).
func WalkVectorFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".json" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conformance: walk %s: %w", dir, err)
	}
	sort.Strings(out)
	return out, nil
}
