package ingestion

import (
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Each storage helper is a new process, and on a small VPS starting one costs
// 0.1–0.4 s (OPS-1, 2 Nov). Two pieces of per-scan memory spare the helpers a
// scan used to start again for an answer it already had.

// readyReuse is how long one root check stands for a scan's later steps. A
// step still reads the source row, and every page and media read checks the
// root identity in its own helper; within this window those reads re-prove
// the root, so the check runs once per window instead of once per step.
// Verification and reconciliation, which decide what is absent, always check.
var readyReuse = 30 * time.Second

type readyProof struct {
	at           time.Time
	generation   int64
	incarnation  string
	rootIdentity string
	health       string
}

type acceptedKey struct {
	job, object, revision string
	policy                int64
}

// acceptedAnalysis is what one analysis observed and published while the
// derived worker had not yet applied it. The queue keeps the file pending, and
// when it is picked again the same observation is accepted again (a duplicate
// of the accepted input) instead of probing the file a second time.
type acceptedAnalysis struct {
	facts     assets.Facts
	completed []string
	receipt   compactcatalog.InfoInputReceipt
}

// maxAcceptedAnalyses bounds the facts kept per process; a file past the bound
// is simply probed again, as before.
const maxAcceptedAnalyses = 4096

func (s *Service) readyStillProven(job string, source catalog.LibrarySource) bool {
	s.reuseMu.Lock()
	defer s.reuseMu.Unlock()
	p, ok := s.ready[job]
	return ok && time.Since(p.at) < readyReuse && p.generation == source.Generation && p.incarnation == source.Incarnation &&
		p.rootIdentity == source.RootIdentity && p.health == source.Health && source.RootIdentity != ""
}

func (s *Service) rememberReady(job string, source catalog.LibrarySource, health string) {
	s.reuseMu.Lock()
	defer s.reuseMu.Unlock()
	if s.ready == nil {
		s.ready = map[string]readyProof{}
	}
	s.ready[job] = readyProof{at: time.Now(), generation: source.Generation, incarnation: source.Incarnation, rootIdentity: source.RootIdentity, health: health}
}

func (s *Service) keepAccepted(key acceptedKey, facts assets.Facts, completed []string) {
	s.reuseMu.Lock()
	defer s.reuseMu.Unlock()
	if s.accepted == nil {
		s.accepted = map[acceptedKey]acceptedAnalysis{}
	}
	if _, ok := s.accepted[key]; !ok && len(s.accepted) >= maxAcceptedAnalyses {
		return
	}
	s.accepted[key] = acceptedAnalysis{facts: facts, completed: append([]string(nil), completed...)}
}

func (s *Service) takeAccepted(key acceptedKey) (acceptedAnalysis, bool) {
	s.forgetReceiptWake(key)
	s.reuseMu.Lock()
	defer s.reuseMu.Unlock()
	a, ok := s.accepted[key]
	delete(s.accepted, key)
	return a, ok
}

// forgetReuse drops a finished scan's reuse state.
func (s *Service) forgetReuse(job string) {
	s.forgetReceiptJob(job)
	s.reuseMu.Lock()
	defer s.reuseMu.Unlock()
	delete(s.ready, job)
	for key := range s.accepted {
		if key.job == job {
			delete(s.accepted, key)
		}
	}
}
