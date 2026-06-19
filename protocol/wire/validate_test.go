package wire

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func ulidAt(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), ulid.DefaultEntropy()).String()
}

func TestULIDTimestamp(t *testing.T) {
	want := time.Now().Truncate(time.Millisecond)
	got, err := ULIDTimestamp(ulidAt(want))
	if err != nil {
		t.Fatalf("ULIDTimestamp: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("timestamp = %v, want %v", got, want)
	}
}

func TestULIDTimestampBad(t *testing.T) {
	if _, err := ULIDTimestamp("not-a-ulid"); err == nil {
		t.Fatal("want error for malformed ULID")
	}
}

func TestCheckSkew(t *testing.T) {
	bus := time.Now().Truncate(time.Millisecond)
	tol := SkewTolerance
	cases := []struct {
		name    string
		offset  time.Duration
		wantErr bool
	}{
		{"in-window", time.Minute, false},
		{"at-boundary", tol, false},
		{"future-beyond", tol + time.Minute, true},
		{"past-beyond", -(tol + time.Minute), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckSkew(ulidAt(bus.Add(c.offset)), bus, tol)
			if c.wantErr {
				if _, ok := errors.AsType[*SkewError](err); !ok {
					t.Fatalf("want *SkewError, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckSkewMalformedULID(t *testing.T) {
	err := CheckSkew("not-a-ulid", time.Now(), SkewTolerance)
	if err == nil {
		t.Fatal("want error for malformed ULID")
	}
	if _, ok := errors.AsType[*SkewError](err); ok {
		t.Fatal("malformed ULID should be a parse error, not *SkewError")
	}
}

func TestCheckEpoch(t *testing.T) {
	if err := CheckEpoch(Epoch, Epoch); err != nil {
		t.Fatalf("equal epochs should pass: %v", err)
	}
	err := CheckEpoch(Epoch, Epoch+1)
	ee, ok := errors.AsType[*EpochError](err)
	if !ok {
		t.Fatalf("want *EpochError, got %v", err)
	}
	if ee.Got != Epoch || ee.Want != Epoch+1 {
		t.Errorf("epoch error fields = %+v", ee)
	}
}

func TestValidate(t *testing.T) {
	good := New("client-1", json.RawMessage(`{"x":1}`))
	if err := good.Validate(); err != nil {
		t.Fatalf("good frame should validate: %v", err)
	}
	bad := map[string]Frame{
		"empty author": {ID: good.ID, Kind: KindMessage, Epoch: Epoch, Record: good.Record},
		"bad id":       {ID: "nope", Author: "a", Kind: KindMessage, Epoch: Epoch, Record: good.Record},
		"wrong kind":   {ID: good.ID, Author: "a", Kind: "event", Epoch: Epoch, Record: good.Record},
		"empty record": {ID: good.ID, Author: "a", Kind: KindMessage, Epoch: Epoch},
		"bad record":   {ID: good.ID, Author: "a", Kind: KindMessage, Epoch: Epoch, Record: json.RawMessage(`{`)},
	}
	for name, e := range bad {
		if err := e.Validate(); err == nil {
			t.Errorf("%s: want validation error, got nil", name)
		}
	}
}
