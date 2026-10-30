package subtitles

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"time"
)

type lazyCacheKey struct{}
type lazyCacheTarget struct {
	resource, discovery, source string
	revision                    int64
}

// ensureCached fills only an already-authorized discovered resource. Publication
// rechecks exact source/inventory evidence, and changes bytes only on cache miss.
func (s *Service) fillCache(ctx context.Context, p identity.Principal, item, id string, revision int64, onlyBurn bool) error {
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return e
	}
	r, e := scanResource(snapshot.Tx().QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id WHERE r.id=? AND v.revision=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id, revision, item))
	if e != nil {
		snapshot.Rollback()
		return e
	}
	if !visible(r, p) {
		snapshot.Rollback()
		return identity.ErrUnauthorized
	}
	if onlyBurn && r.Renderer != "burn_in" || r.digest != "" {
		snapshot.Rollback()
		return nil
	}
	if r.discScoped {
		snapshot.Rollback()
		return s.fillDiscCache(ctx, p, item, r)
	}

	var discoveryRevision int64
	e = snapshot.Tx().QueryRowContext(ctx, `SELECT discovery_revision FROM subtitle_resources WHERE id=? AND current_revision=? AND deleted=0`, id, revision).Scan(&discoveryRevision)
	snapshot.Rollback()
	if e != nil {
		return ErrConflict
	}
	ctx = context.WithValue(ctx, lazyCacheKey{}, lazyCacheTarget{id, r.DiscoveryID, r.SourceID, revision})
	render := "text"
	if r.Format == "ass" || r.Format == "ssa" {
		// A cache fill preserves the discovered track. The player may render
		// it locally or burn it; neither choice discards the authored style.
		render = "styled"
	}
	_, e = s.Import(ctx, p, item, ImportRequest{OperationID: identity.Digest("cache:" + id + ":" + strconv.FormatInt(revision, 10)), SourceID: r.SourceID, DiscoveryID: r.DiscoveryID, ExpectedRevision: discoveryRevision, Scope: "shared", Render: render})
	return e
}

type cacheFill struct {
	done chan struct{}
	err  error
}

func (s *Service) ensureCached(ctx context.Context, p identity.Principal, item, id string, revision int64) error {
	if _, selected := PlaybackSourceInvocationFromContext(ctx); selected {
		// A selected playback continuation cannot outlive its real caller or join
		// detached work admitted for another actor/session. Keep the existing
		// concurrency and outer time bounds; each publication rechecks its seal.
		s.cacheMu.Lock()
		if s.life.Err() != nil {
			s.cacheMu.Unlock()
			return ErrUnavailable
		}
		s.cacheDone.Add(1)
		s.cacheMu.Unlock()
		defer s.cacheDone.Done()
		work, cancel := context.WithTimeout(ctx, 2*time.Hour)
		defer cancel()
		stop := context.AfterFunc(s.life, cancel)
		defer stop()
		select {
		case s.cacheWorkers <- struct{}{}:
			defer func() { <-s.cacheWorkers }()
			return s.fillCache(work, p, item, id, revision, false)
		case <-work.Done():
			return work.Err()
		}
	}
	// Cache fills live beyond a disconnected first reader. Retrying joins the
	// same work instead of restarting a full demux pass.
	key := item + ":" + id + ":" + strconv.FormatInt(revision, 10)
	s.cacheMu.Lock()
	if s.life.Err() != nil {
		s.cacheMu.Unlock()
		return ErrUnavailable
	}
	fill := s.cacheFills[key]
	if fill == nil {
		if len(s.cacheFills) >= 64 {
			s.cacheMu.Unlock()
			return ErrCapacity
		}
		fill = &cacheFill{done: make(chan struct{})}
		s.cacheFills[key] = fill
		s.cacheDone.Add(1)
		supervise.Go("subtitles.cache", func() {
			defer s.cacheDone.Done()
			work, cancel := context.WithTimeout(s.life, 2*time.Hour)
			defer cancel()
			select {
			case s.cacheWorkers <- struct{}{}:
				fill.err = s.fillCache(work, p, item, id, revision, false)
				<-s.cacheWorkers
			case <-work.Done():
				fill.err = work.Err()
			}
			s.cacheMu.Lock()
			delete(s.cacheFills, key)
			close(fill.done)
			s.cacheMu.Unlock()
		})
	}
	s.cacheMu.Unlock()
	select {
	case <-fill.done:
		return fill.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) ensureBurnCached(ctx context.Context, p identity.Principal, item, id string, revision int64) error {
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return e
	}
	r, e := scanResource(snapshot.Tx().QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id WHERE r.id=? AND v.revision=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id, revision, item))
	snapshot.Rollback()
	if e != nil {
		return e
	}
	if r.Renderer != "burn_in" {
		return nil
	}
	return s.ensureCached(ctx, p, item, id, revision)
}
