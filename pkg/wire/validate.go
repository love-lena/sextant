package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ULIDTimestamp extracts the embedded millisecond timestamp from a ULID string.
func ULIDTimestamp(id string) (time.Time, error) {
	u, err := ulid.Parse(id)
	if err != nil {
		return time.Time{}, fmt.Errorf("wire: parse ULID %q: %w", id, err)
	}
	return ulid.Time(u.Time()), nil
}

// SkewError reports that a ULID's embedded timestamp is implausibly far from
// the bus-stamped time. The SDK quarantines and flags the offending message.
type SkewError struct {
	ID        string
	ULIDTime  time.Time
	BusTime   time.Time
	Skew      time.Duration // absolute
	Tolerance time.Duration
}

func (e *SkewError) Error() string {
	return fmt.Sprintf("wire: ULID %s clock skew %s exceeds tolerance %s (ulid=%s bus=%s)",
		e.ID, e.Skew, e.Tolerance,
		e.ULIDTime.UTC().Format(time.RFC3339Nano), e.BusTime.UTC().Format(time.RFC3339Nano))
}

// CheckSkew reports whether the ULID's embedded timestamp is within tolerance
// of busTime. The sender checks before publishing and the receiver checks on
// consume; there is no central enforcer. A non-nil error is a *SkewError when
// the timestamp is out of tolerance, or a parse error for a malformed ULID.
func CheckSkew(id string, busTime time.Time, tolerance time.Duration) error {
	ut, err := ULIDTimestamp(id)
	if err != nil {
		return err
	}
	skew := ut.Sub(busTime)
	if skew < 0 {
		skew = -skew
	}
	if skew > tolerance {
		return &SkewError{ID: id, ULIDTime: ut, BusTime: busTime, Skew: skew, Tolerance: tolerance}
	}
	return nil
}

// EpochError reports a protocol-epoch mismatch.
type EpochError struct {
	Got  int
	Want int
}

func (e *EpochError) Error() string {
	return fmt.Sprintf("wire: protocol epoch mismatch: got %d, want %d", e.Got, e.Want)
}

// CheckEpoch verifies a message's epoch exactly matches the bus epoch. A
// mismatch fails loud (ADR-0010): the SDK refuses with an actionable error.
func CheckEpoch(msgEpoch, busEpoch int) error {
	if msgEpoch != busEpoch {
		return &EpochError{Got: msgEpoch, Want: busEpoch}
	}
	return nil
}

// Validate checks the frame is structurally well-formed: a parseable ULID id, a
// non-empty author, a known kind, and a non-empty, syntactically valid JSON
// record. It does not check the epoch — that is contextual (see CheckEpoch),
// because durable streams outlive epochs.
func (f Frame) Validate() error {
	if _, err := ulid.Parse(f.ID); err != nil {
		return fmt.Errorf("wire: invalid id: %w", err)
	}
	if f.Author == "" {
		return errors.New("wire: empty author")
	}
	if f.Kind != KindMessage && f.Kind != KindArtifact {
		return fmt.Errorf("wire: unknown kind %q", f.Kind)
	}
	if len(f.Record) == 0 || !json.Valid(f.Record) {
		return errors.New("wire: record must be non-empty valid JSON")
	}
	return nil
}
