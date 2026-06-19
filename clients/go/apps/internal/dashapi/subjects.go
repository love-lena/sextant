package dashapi

import (
	"context"
	"net/http"
	"sort"

	"github.com/love-lena/sextant/clients/go/sdk"
)

// subjectStat is one observed subject and how many frames the dash has seen on
// it this run — enough for the UI to list and order conversations on load.
type subjectStat struct {
	Subject string `json:"subject"`
	Count   uint64 `json:"count"`
}

// Watch starts a standing subscription to every message subject (msg.>) and
// records each subject the dash observes, so /api/subjects can list the
// conversations it knows about — including ones whose traffic predates a given
// browser tab. It stops when ctx is cancelled (server shutdown). Best-effort: a
// subscribe failure is returned for the caller to log, not treated as fatal.
func (s *Server) Watch(ctx context.Context) error {
	sub, err := s.bus.Subscribe(ctx, "msg.>", func(m sextant.Message) {
		s.subjMu.Lock()
		s.subjects[m.Subject]++
		s.subjMu.Unlock()
	}, sextant.DeliverAll())
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		sub.Stop()
	}()
	return nil
}

// handleSubjects lists the subjects the dash has seen this run, sorted, so the
// UI can populate its conversation list on load rather than only as new traffic
// arrives. The contents are subjects, not message bodies — discovery, not reads.
func (s *Server) handleSubjects(w http.ResponseWriter, r *http.Request) {
	s.subjMu.Lock()
	out := make([]subjectStat, 0, len(s.subjects))
	for subj, c := range s.subjects {
		out = append(out, subjectStat{Subject: subj, Count: c})
	}
	s.subjMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	writeJSON(w, http.StatusOK, out)
}
