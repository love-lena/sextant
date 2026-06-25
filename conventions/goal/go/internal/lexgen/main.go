// Command lexgen generates the Go record types for the goals convention from
// the goal lexicon (protocol/lexicons/goal.json) — the net-new piece ADR-0041's
// "record types are generated from the lexicon per language" calls for.
//
// It is deliberately NOT a universal lexicon→Go engine. It generates exactly the
// goals records: the goal record's object and its one nested object-array
// (criteria → Criterion). The bright line is "concept, not codegen" — only the
// record TYPES are generated; the verb LOGIC in conv/goals is hand-written. A
// second convention that needs generated types gets its own small generator (or
// this one generalised against that real second case), not a speculative engine
// built before there is a second user.
//
// Run it through `go generate ./conventions/goal/go/...` or `make
// generate`; see the //go:generate directive in goals.go. The output is
// goal_gen.go in the goals package, committed, with a generated-file header
// naming this tool and the source lexicon so a fresh reader knows not to hand-edit
// it and where it comes from.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"strings"
)

// lexiconFile is the goal lexicon, relative to the goal package dir this runs
// in (go generate sets cwd to the directive's package: conventions/goal/go).
const lexiconFile = "../../../protocol/lexicons/goal.json"

// outputFile is the generated types file, in the goals package dir.
const outputFile = "goal_gen.go"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lexgen:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(lexiconFile)
	if err != nil {
		return fmt.Errorf("read lexicon: %w", err)
	}
	rec, err := parseRecord(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", lexiconFile, err)
	}
	src, err := render(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputFile, src, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputFile, err)
	}
	return nil
}

// --- lexicon parsing (the goal record def) ---

// objectDef is a lexicon object: its required field set and its properties in
// authored order (order is preserved so the generated struct reads like the
// lexicon).
type objectDef struct {
	required map[string]bool
	props    []property
}

type property struct {
	name string
	typ  string     // lexicon type: "string", "array", "object"
	desc string     // the lexicon description, carried onto the field as a doc comment
	item *objectDef // for type=="array" of objects: the item object def
}

// parseRecord extracts the goal record def (defs.main.record) into an objectDef.
func parseRecord(raw []byte) (objectDef, error) {
	var lf struct {
		Defs struct {
			Main struct {
				Record json.RawMessage `json:"record"`
			} `json:"main"`
		} `json:"defs"`
	}
	if err := json.Unmarshal(raw, &lf); err != nil {
		return objectDef{}, err
	}
	return parseObject(lf.Defs.Main.Record)
}

// parseObject decodes one lexicon object def: its required set and ordered
// properties. Property order is taken from the raw JSON (json.RawMessage of the
// properties block re-decoded key-by-key) so the generated fields mirror the
// authored order rather than Go's map iteration.
func parseObject(raw json.RawMessage) (objectDef, error) {
	var od struct {
		Required   []string        `json:"required"`
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &od); err != nil {
		return objectDef{}, err
	}
	out := objectDef{required: map[string]bool{}}
	for _, r := range od.Required {
		out.required[r] = true
	}
	pairs, err := orderedKeys(od.Properties)
	if err != nil {
		return objectDef{}, err
	}
	for _, key := range pairs {
		var pv struct {
			Type        string          `json:"type"`
			Description string          `json:"description"`
			Items       json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(rawFor(od.Properties, key), &pv); err != nil {
			return objectDef{}, err
		}
		p := property{name: key, typ: pv.Type, desc: pv.Description}
		if pv.Type == "array" && len(bytes.TrimSpace(pv.Items)) > 0 {
			var itemType struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(pv.Items, &itemType)
			if itemType.Type == "object" {
				item, err := parseObject(pv.Items)
				if err != nil {
					return objectDef{}, fmt.Errorf("array %q items: %w", key, err)
				}
				p.item = &item
			}
		}
		out.props = append(out.props, p)
	}
	return out, nil
}

// orderedKeys returns the keys of a JSON object in authored order.
func orderedKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("object key is %T, not a string", k)
		}
		keys = append(keys, key)
		// Skip the value.
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// rawFor returns the raw value of key within a JSON object.
func rawFor(obj json.RawMessage, key string) json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(obj, &m)
	return m[key]
}

// --- rendering ---

// render emits the goal_gen.go source: a Goal struct for the record and a
// Criterion struct for the criteria item, both with json tags matching the
// lexicon field names exactly (the whole point — the types cannot drift from the
// contract). Non-required fields take omitempty.
func render(rec objectDef) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated by conventions/goal/go/internal/lexgen from\n")
	b.WriteString("// protocol/lexicons/goal.json. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// The record types are generated so they cannot silently drift from the\n")
	b.WriteString("// lexicon contract (ADR-0041). Regenerate with `make generate` (or\n")
	b.WriteString("// `go generate ./conventions/goal/go/...`) after editing the lexicon;\n")
	b.WriteString("// the verb logic that uses these types is hand-written in goals.go.\n\n")
	b.WriteString("package goals\n\n")

	// The criteria item type, if any, named by singularising the array field.
	needsJSON := false
	var body strings.Builder
	for _, p := range rec.props {
		if p.item != nil {
			renderStruct(&body, singular(p.name),
				"is one acceptance criterion of a goal (the criteria array item).",
				*p.item, &needsJSON)
			body.WriteString("\n")
		}
	}
	renderStruct(&body, "Goal",
		"is the goal record (protocol/lexicons/goal.json): a north-star plus its "+
			"acceptance criteria. Goal STATUS is derived from the criteria rollup, "+
			"never stored — see Status in goals.go.", rec, &needsJSON)

	if needsJSON {
		b.WriteString("import \"encoding/json\"\n\n")
	}
	b.WriteString(body.String())

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("gofmt generated source: %w\n%s", err, b.String())
	}
	return formatted, nil
}

// renderStruct writes one struct. needsJSON is flipped true when a field falls
// back to json.RawMessage, so render knows to emit the encoding/json import.
func renderStruct(b *strings.Builder, name, doc string, od objectDef, needsJSON *bool) {
	fmt.Fprintf(b, "// %s %s\n", name, doc)
	fmt.Fprintf(b, "type %s struct {\n", name)
	for _, p := range od.props {
		if p.desc != "" {
			fmt.Fprintf(b, "\t// %s\n", oneLine(p.desc))
		}
		gt := goType(p)
		if strings.Contains(gt, "json.RawMessage") {
			*needsJSON = true
		}
		fmt.Fprintf(b, "\t%s %s `json:\"%s%s\"`\n", goName(p.name), gt, p.name, omit(od.required[p.name]))
	}
	b.WriteString("}\n")
}

// goType maps a lexicon property to its Go type. Strings are string; an array of
// objects is a slice of the singularised item type; any other array or an object
// falls back to json.RawMessage (opaque), which keeps the generator minimal
// without dropping unknown shapes. The goal lexicon only exercises string and
// the criteria object-array today.
func goType(p property) string {
	switch p.typ {
	case "string":
		return "string"
	case "array":
		if p.item != nil {
			return "[]" + singular(p.name)
		}
		return "[]json.RawMessage"
	default:
		return "json.RawMessage"
	}
}

func omit(required bool) string {
	if required {
		return ""
	}
	return ",omitempty"
}

// initialisms are field words Go convention writes all-caps (id → ID). The set
// is small on purpose — the goal lexicon only needs `id`; extend it against a
// real second field, not speculatively.
var initialisms = map[string]string{"id": "ID"}

// goName turns a lexicon field name into an exported Go field name: upper-case
// the first letter of each separator-delimited word, with known initialisms
// written all-caps (so `id` becomes `ID`, the Go-idiomatic form, not `Id`). The
// goal lexicon uses simple single-word names; this stays minimal.
func goName(field string) string {
	if field == "" {
		return field
	}
	parts := strings.FieldsFunc(field, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for i, part := range parts {
		if up, ok := initialisms[strings.ToLower(part)]; ok {
			parts[i] = up
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

// singular turns an array field name into its item type name (criteria →
// Criterion). It handles the one irregular plural the goal lexicon uses and a
// simple trailing-s fallback; a richer pluraliser is unwarranted for one field.
func singular(field string) string {
	switch field {
	case "criteria":
		return "Criterion"
	}
	name := goName(field)
	return strings.TrimSuffix(name, "s")
}

// oneLine collapses a multi-line lexicon description to a single comment line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}
