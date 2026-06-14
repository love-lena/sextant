package sx_test

import (
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/sx"
)

// DMSubject is the deterministic 2-party DM topic convention (ADR-0034): a DM is
// a topic with exactly two participants, named by their two ULIDs in sorted
// order so both sides compute the identical subject without coordination.
func TestDMSubject(t *testing.T) {
	a := "01KTXBYMJN8X4FZ8HJPF5XJJ0A"
	b := "01KTYDS669GQHBARC4FSTCDATQ" // a < b lexicographically

	want := "msg.topic.dm.01KTXBYMJN8X4FZ8HJPF5XJJ0A.01KTYDS669GQHBARC4FSTCDATQ"
	if got := sx.DMSubject(a, b); got != want {
		t.Errorf("DMSubject(a,b) = %q, want %q", got, want)
	}

	// Order-independent: the two participants must derive the same subject
	// regardless of which one they pass first.
	if got := sx.DMSubject(b, a); got != want {
		t.Errorf("DMSubject(b,a) = %q, want %q (must be order-independent)", got, want)
	}
}

// A DM topic is a topic: it must live under the same topic subject space as
// TopicSubject, so anyone watching msg.topic.> sees DMs too.
func TestDMSubjectIsATopic(t *testing.T) {
	s := sx.DMSubject("zzz", "aaa")
	prefix := sx.TopicSubject("") // "msg.topic."
	if !strings.HasPrefix(s, prefix) {
		t.Errorf("DMSubject %q is not under the topic space %q", s, prefix)
	}
	if !strings.HasPrefix(s, sx.TopicSubject("dm.")) {
		t.Errorf("DMSubject %q is not under the dm. namespace %q", s, sx.TopicSubject("dm."))
	}
}
