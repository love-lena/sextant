package conformance_test

import (
	"encoding/json"
	"testing"

	conf "github.com/love-lena/sextant/protocol/conformance"
)

// TestCanonicalize pins the canonical-JSON rule a TS implementer must reproduce
// byte-for-byte to capture identical vectors. Each case is an input and its one
// canonical form.
func TestCanonicalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"object keys sorted", `{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{"nested keys sorted", `{"z":{"y":1,"x":2}}`, `{"z":{"x":2,"y":1}}`},
		{"whitespace stripped", "{\n  \"a\" : 1\n}", `{"a":1}`},
		{"arrays keep order", `[3,1,2]`, `[3,1,2]`},
		{"integers minimal", `{"n": 1.0}`, `{"n":1}`},
		{"trailing zero fraction", `{"n": 1.50}`, `{"n":1.5}`},
		{"exponent expanded", `{"n": 1e2}`, `{"n":100}`},
		{"negative integer", `{"n": -7.0}`, `{"n":-7}`},
		{"large integer preserved", `{"seq": 9007199254740993}`, `{"seq":9007199254740993}`},
		{"html chars not escaped", `{"t":"a<b>c&d"}`, `{"t":"a<b>c&d"}`},
		{"unicode preserved", `{"name":"héllo→世界"}`, `{"name":"héllo→世界"}`},
		{"control char escaped", "{\"t\":\"a\\nb\"}", `{"t":"a\nb"}`},
		{"null/bool", `{"a":null,"b":true,"c":false}`, `{"a":null,"b":true,"c":false}`},
		{"empty object", `{}`, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := conf.Canonicalize(json.RawMessage(c.in))
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("canonicalize(%s):\n got  %s\n want %s", c.in, got, c.want)
			}
		})
	}
}

// TestCanonicalizeIdempotent: canonicalizing an already-canonical form is a
// no-op, so re-recording a vector never churns it.
func TestCanonicalizeIdempotent(t *testing.T) {
	once, err := conf.Canonicalize(json.RawMessage(`{"b":[1,2,{"d":4,"c":3}],"a":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := conf.Canonicalize(once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("not idempotent:\n once  %s\n twice %s", once, twice)
	}
}

// TestCanonicalEqual: two payloads differing only in key order / whitespace are
// equal; differing values are not. Absent payloads compare equal (both null).
func TestCanonicalEqual(t *testing.T) {
	eq, err := conf.CanonicalEqual(json.RawMessage(`{"a":1,"b":2}`), json.RawMessage("{ \"b\" : 2, \"a\" : 1 }"))
	if err != nil {
		t.Fatal(err)
	}
	if !eq {
		t.Error("reordered/whitespaced payloads should be canonical-equal")
	}
	ne, err := conf.CanonicalEqual(json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if ne {
		t.Error("differing values should not be canonical-equal")
	}
	none, err := conf.CanonicalEqual(nil, json.RawMessage(""))
	if err != nil {
		t.Fatal(err)
	}
	if !none {
		t.Error("two absent payloads should be canonical-equal (both null)")
	}
}

// TestValidate rejects vectors missing the contract fields.
func TestValidate(t *testing.T) {
	good := conf.OpTranscriptVector{
		Epoch:      1,
		Convention: "goals",
		Verb:       "setCriterion",
		Operations: []conf.Op{{Op: "artifact.update", Name: "goal.x"}},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good vector rejected: %v", err)
	}
	bad := []conf.OpTranscriptVector{
		{Epoch: 1, Verb: "v"},        // no convention
		{Epoch: 1, Convention: "c"},  // no verb
		{Convention: "c", Verb: "v"}, // no epoch
		{Epoch: 1, Convention: "c", Verb: "v", Operations: []conf.Op{{}}}, // op with no name
	}
	for i, v := range bad {
		if err := v.Validate(); err == nil {
			t.Errorf("bad vector %d accepted", i)
		}
	}
}
