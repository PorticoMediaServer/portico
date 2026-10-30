package catalog

import "strconv"

// Exact counts are cached only against both durable revisions and the
// viewer's progress counter. Progress and source changes invalidate them; the
// FIFO cap bounds memory across viewers.
//
// The progress counter is part of the key because the viewer revision no
// longer moves on every progress write: a position that crosses the end, or a
// dismissed title played again, changes this count without changing which
// titles the viewer has in progress.
func (s *Service) continueCount(library, profile string) (int, error) {
	revision, e := s.ContentRevision(library, profile)
	if e != nil {
		return 0, e
	}
	observed, e := s.progressObservation(library, profile)
	if e != nil {
		return 0, e
	}
	key := countKey{library, profile, revision, "movie:" + strconv.FormatInt(observed, 10)}
	if cached, ok := s.cachedCount(key); ok {
		return cached, nil
	}
	var count int
	e = s.read().QueryRow(`SELECT count(*) FROM progress_activity a JOIN progress p ON p.profile_id=a.profile_id AND p.item_id=a.item_id JOIN catalog_entities i ON i.id=p.item_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE a.profile_id=? AND a.library_id=? AND cl.library_id=a.library_id AND i.kind=1 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id) AND a.state!='ended' AND NOT EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=p.profile_id AND d.item_id=p.item_id AND d.playback_id=p.playback_id) AND p.position>0 AND p.position<(SELECT max(s.duration)*1000-3000 FROM catalog_asset_links l JOIN catalog_assets s ON s.id=l.asset_id WHERE l.entity_id=i.id AND s.available=1)`, profile, library).Scan(&count)
	if e != nil {
		return 0, e
	}
	after, e := s.ContentRevision(library, profile)
	if e != nil {
		return 0, e
	}
	if after != revision {
		return 0, ErrStaleContinuation
	}
	// Progress written while counting is not a conflict: the count is right
	// as read, and is simply not kept for the counter it no longer matches.
	if now, e := s.progressObservation(library, profile); e != nil {
		return 0, e
	} else if now == observed {
		s.storeCount(key, count)
	}
	return count, nil
}

// progressObservation is the counter every progress write of this viewer in
// this library moves.
func (s *Service) progressObservation(library, profile string) (int64, error) {
	var n int64
	e := s.read().QueryRow(`SELECT COALESCE((SELECT observation_revision FROM viewer_revisions WHERE profile_id=? AND library_id=?),0)`, profile, library).Scan(&n)
	return n, e
}
