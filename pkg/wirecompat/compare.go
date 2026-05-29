// Package wirecompat implements the AUTHORING limb of the WireEpoch
// compatibility check (control-plane RFC §5.8): a mechanical diff of two
// snapshots of pkg/sextantproto/schemas/ that flags BREAKING wire changes
// and decides whether the WireEpoch must bump.
//
// Breaking (a peer on the old schema would misread/reject the new wire):
//   - a property removed from a message
//   - a property's type changed
//   - a property that was optional became required
//   - a whole message (schema file) removed
//   - a value removed from a closed enum (kinds / address_kinds /
//     frame_kinds in wire.json)
//
// Additive (non-breaking): a new optional property, a new enum value, a
// new message. proto_version is cosmetic and ignored.
//
// The CI gate (cmd/sextant-schema-compat) regenerates the schemas from the
// current Go source, compares against the committed snapshot, and fails the
// build if Result.NeedsBump() is true.
package wirecompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Result is the verdict of a Compare.
type Result struct {
	// Breaking lists human-readable descriptions of every breaking change
	// found. Empty means the diff is additive-only.
	Breaking []string
	// OldEpoch / NewEpoch are the wire_epoch from each snapshot's wire.json.
	OldEpoch int
	NewEpoch int
}

// NeedsBump reports whether the gate must FAIL: a breaking change landed
// but the epoch did not advance. Ambiguity (a breaking change with no
// epoch movement) errs toward requiring the bump, per RFC §5.8.
func (r Result) NeedsBump() bool {
	return len(r.Breaking) > 0 && r.NewEpoch <= r.OldEpoch
}

// wireManifest is the subset of wire.json this package reads.
type wireManifest struct {
	WireEpoch    int      `json:"wire_epoch"`
	Kinds        []string `json:"kinds"`
	AddressKinds []string `json:"address_kinds"`
	FrameKinds   []string `json:"frame_kinds"`
}

// Compare diffs the schema snapshot in oldDir against newDir and returns
// the breaking changes plus both epochs.
func Compare(oldDir, newDir string) (Result, error) {
	var res Result

	oldWire, err := loadManifest(oldDir)
	if err != nil {
		return res, fmt.Errorf("old wire.json: %w", err)
	}
	newWire, err := loadManifest(newDir)
	if err != nil {
		return res, fmt.Errorf("new wire.json: %w", err)
	}
	res.OldEpoch = oldWire.WireEpoch
	res.NewEpoch = newWire.WireEpoch

	// Closed-enum removals (kinds / address_kinds / frame_kinds).
	res.Breaking = append(res.Breaking, removedValues("kind", oldWire.Kinds, newWire.Kinds)...)
	res.Breaking = append(res.Breaking, removedValues("address_kind", oldWire.AddressKinds, newWire.AddressKinds)...)
	res.Breaking = append(res.Breaking, removedValues("frame_kind", oldWire.FrameKinds, newWire.FrameKinds)...)

	// Per-schema structural diff.
	oldSchemas, err := loadSchemas(oldDir)
	if err != nil {
		return res, fmt.Errorf("old schemas: %w", err)
	}
	newSchemas, err := loadSchemas(newDir)
	if err != nil {
		return res, fmt.Errorf("new schemas: %w", err)
	}

	names := make([]string, 0, len(oldSchemas))
	for name := range oldSchemas {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		newDoc, ok := newSchemas[name]
		if !ok {
			res.Breaking = append(res.Breaking, fmt.Sprintf("%s: message removed", name))
			continue
		}
		res.Breaking = append(res.Breaking, diffSchema(name, oldSchemas[name], newDoc)...)
	}

	return res, nil
}

func loadManifest(dir string) (wireManifest, error) {
	var m wireManifest
	// dir is a generator-controlled schema snapshot (a temp dir or the
	// committed schemas/), not attacker input — this is a dev/CI tool.
	raw, err := os.ReadFile(filepath.Join(dir, "wire.json")) //nolint:gosec // dir is generator-controlled
	if errors.Is(err, os.ErrNotExist) {
		// A baseline predating wire.json (e.g. the first PR that adds it):
		// treat as epoch 0 with no declared enums. No enum-removal can be
		// detected against an absent baseline, and epoch 0 < any real epoch,
		// so a genuine breaking change still trips the bump requirement.
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("parse wire.json: %w", err)
	}
	return m, nil
}

// loadSchemas reads every *.json file except wire.json into a map of
// filename → parsed JSON.
func loadSchemas(dir string) (map[string]map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" || e.Name() == "wire.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // dir is generator-controlled
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		out[e.Name()] = doc
	}
	return out, nil
}

// removedValues returns a breaking entry for every value present in old
// but absent from new (a removed closed-enum value).
func removedValues(label string, oldVals, newVals []string) []string {
	present := make(map[string]bool, len(newVals))
	for _, v := range newVals {
		present[v] = true
	}
	var out []string
	for _, v := range oldVals {
		if !present[v] {
			out = append(out, fmt.Sprintf("wire.json: removed %s value %q", label, v))
		}
	}
	return out
}

// diffSchema compares the $defs of two schema documents for the same file.
// Every named definition's properties and required set are diffed.
func diffSchema(file string, oldDoc, newDoc map[string]any) []string {
	var out []string

	oldDefs := defsOf(oldDoc)
	newDefs := defsOf(newDoc)

	defNames := make([]string, 0, len(oldDefs))
	for name := range oldDefs {
		defNames = append(defNames, name)
	}
	sort.Strings(defNames)

	for _, defName := range defNames {
		oldDef := asObject(oldDefs[defName])
		newDefRaw, ok := newDefs[defName]
		if !ok {
			out = append(out, fmt.Sprintf("%s: type %s removed", file, defName))
			continue
		}
		newDef := asObject(newDefRaw)
		out = append(out, diffDef(file, defName, oldDef, newDef)...)
	}
	return out
}

func diffDef(file, defName string, oldDef, newDef map[string]any) []string {
	var out []string

	oldProps := asObject(oldDef["properties"])
	newProps := asObject(newDef["properties"])
	oldReq := stringSet(oldDef["required"])
	newReq := stringSet(newDef["required"])

	propNames := make([]string, 0, len(oldProps))
	for name := range oldProps {
		propNames = append(propNames, name)
	}
	sort.Strings(propNames)

	for _, p := range propNames {
		newProp, ok := newProps[p]
		if !ok {
			out = append(out, fmt.Sprintf("%s (%s): removed property %q", file, defName, p))
			continue
		}
		if t := typeChange(asObject(oldProps[p]), asObject(newProp)); t != "" {
			out = append(out, fmt.Sprintf("%s (%s): property %q %s", file, defName, p, t))
		}
	}

	// optional → required is breaking (old peers may omit the field).
	reqNames := make([]string, 0, len(newReq))
	for name := range newReq {
		reqNames = append(reqNames, name)
	}
	sort.Strings(reqNames)
	for _, p := range reqNames {
		if !oldReq[p] {
			out = append(out, fmt.Sprintf("%s (%s): property %q became required (was optional)", file, defName, p))
		}
	}

	return out
}

// typeChange returns a non-empty description if the property's wire shape
// changed in a breaking way: a changed "type", or a changed "$ref"
// (different referenced definition). Returns "" if unchanged or only
// additive metadata differs.
func typeChange(oldProp, newProp map[string]any) string {
	oldType, newType := oldProp["type"], newProp["type"]
	if fmt.Sprint(oldType) != fmt.Sprint(newType) {
		return fmt.Sprintf("type changed (%v → %v)", oldType, newType)
	}
	oldRef, newRef := oldProp["$ref"], newProp["$ref"]
	if fmt.Sprint(oldRef) != fmt.Sprint(newRef) {
		return fmt.Sprintf("ref changed (%v → %v)", oldRef, newRef)
	}
	return ""
}

func defsOf(doc map[string]any) map[string]any {
	return asObject(doc["$defs"])
}

func asObject(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	arr, ok := v.([]any)
	if !ok {
		return out
	}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out[s] = true
		}
	}
	return out
}
