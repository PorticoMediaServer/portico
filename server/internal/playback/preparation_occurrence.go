package playback

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sync"
	"time"
)

// PreparationOccurrence owns only this request's not-yet-installed child work.
// It attenuates the already authorized parent's exact occurrence; it is not a
// caller permission and cannot select a different parent or renew its grant.
type PreparationOccurrence struct {
	ctx      context.Context
	cancel   context.CancelFunc
	registry *occurrenceRegistry
	entry    *occurrenceEntry
	run      *occurrenceRun
	id       string
	done     chan struct{}
	once     sync.Once
}

func (p *PreparationOccurrence) Context() context.Context { return p.ctx }
func (p *PreparationOccurrence) Close() {
	p.once.Do(func() { p.cancel(); close(p.done); p.registry.finished(p.id, p.entry, p.run) })
}

func (s *Service) RetainPreparation(ctx context.Context, id string, generation int) (*PreparationOccurrence, error) {
	if dbwork.Snapshot(ctx) != nil || generation < 1 {
		return nil, ErrOccurrenceChanged
	}
	registry := s.occurrenceRegistry()
	intent, err := registry.intent(id)
	if err != nil {
		return nil, err
	}
	defer intent.release()
	// Register before the fresh read; the original intent is checked again when
	// the exact run becomes installed. Never read from an earlier snapshot.
	var request, asset string
	var item int64
	err = s.db.QueryRowContext(ctx, `SELECT request_id,item_id,asset_id FROM playback_sessions WHERE id=? AND generation=? AND state NOT IN ('stopped','ended','failed') AND expires_at>?`, id, generation, time.Now().UTC().Format(time.RFC3339)).Scan(&request, &item, &asset)
	if err != nil {
		return nil, err
	}
	work, cancel := context.WithCancel(ctx)
	p := &PreparationOccurrence{cancel: cancel, registry: registry, entry: intent.entry, id: id, done: make(chan struct{})}
	p.run = &occurrenceRun{generation: generation, requestID: request, itemID: item, assetID: asset, cancel: cancel, done: p.done}
	p.ctx = withOccurrenceContext(work, s.db, registry, id, p.entry, p.run)
	if err = intent.install(p.run, func() {}); err != nil {
		cancel()
		return nil, err
	}
	return p, nil
}
