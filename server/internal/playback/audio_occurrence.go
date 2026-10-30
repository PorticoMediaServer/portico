package playback

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"sync"
	"time"
)

type prefetchRecord struct {
	service    *Service
	id         string
	generation int
	used       int64
	readers    int
	retired    bool
}

func (r *prefetchRecord) retire() {
	s := r.service
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	r.retired = true
	if r.readers == 0 && s.prefetchOwned[r.id] == r {
		delete(s.prefetchOwned, r.id)
	}
}

// AudioOccurrence holds an original generation's reader and budget token. Late
// refunds/releases operate only on this record, never a successor found by id.
type AudioOccurrence struct {
	ctx     context.Context
	cancel  context.CancelFunc
	service *Service
	record  *prefetchRecord
	run     *occurrenceRun
	entry   *occurrenceEntry
	once    sync.Once
	done    chan struct{}
}

func (a *AudioOccurrence) Context() context.Context { return a.ctx }
func (a *AudioOccurrence) Close() {
	a.once.Do(func() {
		a.cancel()
		s, r := a.service, a.record
		s.prefetchMu.Lock()
		r.readers--
		if r.retired && r.readers == 0 && s.prefetchOwned[r.id] == r {
			delete(s.prefetchOwned, r.id)
		}
		retired := r.retired && r.readers == 0
		s.prefetchMu.Unlock()
		registry := s.occurrenceRegistry()
		if retired {
			registry.mu.Lock()
			delete(a.entry.budgets, r)
			registry.mu.Unlock()
		}
		close(a.done)
		registry.finished(r.id, a.entry, a.run)
	})
}
func (a *AudioOccurrence) Take(want, limit int64) int64 {
	if want <= 0 || limit <= 0 || a.ctx.Err() != nil {
		return 0
	}
	s, r := a.service, a.record
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	if r.retired || s.prefetchOwned[r.id] != r {
		return 0
	}
	n := min(want, max(int64(0), limit-r.used))
	r.used += n
	return n
}
func (a *AudioOccurrence) Charge(n, limit int64) bool {
	if n < 0 || limit < 0 || a.ctx.Err() != nil {
		return false
	}
	s, r := a.service, a.record
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	if r.retired || s.prefetchOwned[r.id] != r || n > limit-r.used {
		return false
	}
	r.used += n
	return true
}
func (a *AudioOccurrence) Refund(n int64) {
	if n <= 0 {
		return
	}
	s, r := a.service, a.record
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	r.used = max(int64(0), r.used-n)
}

func (s *Service) RetainAudioOccurrence(ctx context.Context, grant, id string, generation int) (*AudioOccurrence, error) {
	if dbwork.Snapshot(ctx) != nil || generation < 1 {
		return nil, ErrOccurrenceChanged
	}
	registry := s.occurrenceRegistry()
	intent, err := registry.intent(id)
	if err != nil {
		return nil, err
	}
	defer intent.release()
	// Registration precedes both the permission and durable reads. No stale
	// snapshot can create a new entry after a committed entry has been pruned.
	if _, _, _, err = s.ResolveDecodeGrantContext(ctx, grant); err != nil {
		return nil, err
	}
	var request, asset string
	var private bool
	var item int64
	err = s.db.QueryRowContext(ctx, `SELECT request_id,item_id,asset_id,EXISTS(SELECT 1 FROM playback_private_presentations WHERE session_id=playback_sessions.id) FROM playback_sessions WHERE id=? AND generation=? AND grant_hash=? AND state NOT IN ('stopped','ended','failed') AND expires_at>?`, id, generation, identity.Digest(grant), time.Now().UTC().Format(time.RFC3339)).Scan(&request, &item, &asset, &private)
	if err != nil {
		return nil, err
	}
	reader, cancel := context.WithCancel(ctx)
	a := &AudioOccurrence{ctx: reader, cancel: cancel, service: s, entry: intent.entry, done: make(chan struct{})}
	a.run = &occurrenceRun{generation: generation, requestID: request, itemID: item, assetID: asset, cancel: cancel, done: a.done}
	a.ctx = withOccurrenceContext(reader, s.db, registry, id, a.entry, a.run)
	s.prefetchMu.Lock()
	if s.prefetchOwned == nil {
		s.prefetchOwned = make(map[string]*prefetchRecord)
	}
	var record *prefetchRecord
	if private {
		record = s.prefetchOwned[id]
	}
	previous := record
	if record == nil || record.generation != generation {
		if private && record == nil && len(s.prefetchOwned) >= 4096 {
			s.prefetchMu.Unlock()
			cancel()
			return nil, ErrOccurrenceChanged
		}
		record = &prefetchRecord{service: s, id: id, generation: generation}
	}
	if record.retired {
		s.prefetchMu.Unlock()
		cancel()
		return nil, ErrOccurrenceChanged
	}
	a.record = record
	err = intent.install(a.run, func() {
		record.readers++
		if !private {
			return
		}
		s.prefetchOwned[id] = record
		if previous != nil && previous != record {
			previous.retired = true
			if previous.readers == 0 {
				delete(intent.entry.budgets, previous)
			}
		}
		if intent.entry.budgets == nil {
			intent.entry.budgets = make(map[*prefetchRecord]struct{})
		}
		intent.entry.budgets[record] = struct{}{}
	})
	s.prefetchMu.Unlock()
	if err != nil {
		cancel()
		return nil, err
	}
	return a, nil
}
