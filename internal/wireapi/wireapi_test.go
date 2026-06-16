package wireapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/love-lena/sextant/internal/wireapi"
)

// The heartbeat op name contains a dot ("clients.heartbeat"); the call subject
// must round-trip through CallSubject/ParseCallSubject like any other op.
func TestHeartbeatCallSubjectRoundTrip(t *testing.T) {
	const id = "01KAGENTSPIKE0000000000000"
	subj := wireapi.CallSubject(id, wireapi.OpClientsHeartbeat)
	if want := "sx.api." + id + ".clients.heartbeat"; subj != want {
		t.Fatalf("CallSubject = %q, want %q", subj, want)
	}
	gotID, gotOp, ok := wireapi.ParseCallSubject(subj)
	if !ok {
		t.Fatalf("ParseCallSubject(%q) not ok", subj)
	}
	if gotID != id || gotOp != wireapi.OpClientsHeartbeat {
		t.Fatalf("ParseCallSubject = (%q, %q), want (%q, %q)", gotID, gotOp, id, wireapi.OpClientsHeartbeat)
	}
}

// LastSeen is additive: it round-trips when set and is omitted when empty (so a
// pre-TASK-126 record / reply is unchanged on the wire).
func TestClientEntryLastSeenOmitEmpty(t *testing.T) {
	noBeat, err := json.Marshal(wireapi.ClientEntry{ID: "01X", DisplayName: "a", Kind: "agent", IssuedAt: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(noBeat), "last_seen") {
		t.Fatalf("empty LastSeen must be omitted, got %s", noBeat)
	}

	withBeat := wireapi.ClientEntry{ID: "01X", LastSeen: "2026-06-16T04:00:00Z"}
	b, err := json.Marshal(withBeat)
	if err != nil {
		t.Fatal(err)
	}
	var rt wireapi.ClientEntry
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.LastSeen != withBeat.LastSeen {
		t.Fatalf("LastSeen round-trip = %q, want %q", rt.LastSeen, withBeat.LastSeen)
	}
}

func TestHeartbeatRecordsRoundTrip(t *testing.T) {
	for _, tc := range []any{
		wireapi.HeartbeatInput{Seq: 7},
		wireapi.HeartbeatOutput{ServerTime: "2026-06-16T04:00:00Z"},
		wireapi.HeartbeatEcho{Seq: 7},
	} {
		b, err := json.Marshal(tc)
		if err != nil {
			t.Fatalf("marshal %T: %v", tc, err)
		}
		if len(b) == 0 || string(b) == "{}" && tc != (wireapi.HeartbeatInput{}) {
			t.Fatalf("marshal %T produced %s", tc, b)
		}
	}
}
