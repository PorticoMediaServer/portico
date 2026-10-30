package metadata

import (
	"context"
	"database/sql"
)

// nextArtworkCompaction finds the next source whose large slot was made under
// a larger edge than the current policy. The walk is keyed by the large-edge
// index; its cursor is disposable scheduling progress, reset by a policy change
// or a new Service, never persisted authority.
func (s *Service) nextArtworkCompaction(ctx context.Context, policy artworkEncodingPolicy) (string, error) {
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	if s.compactionEdge != policy.edge {
		s.compactionEdge, s.compactionCursor, s.compactionCursorEdge = policy.edge, "", 0
		s.compactionDone = false
	}
	// New acquisitions already capture this policy, so a completed walk cannot
	// gain candidates until the policy changes again.
	if s.compactionDone {
		return "", sql.ErrNoRows
	}
	var digest string
	var edge int
	err := s.db.QueryRowContext(ctx, `SELECT s.digest,s.large_edge FROM artwork_sources s INDEXED BY artwork_sources_large_edge WHERE s.large_edge>?1 AND (s.large_edge>?2 OR s.large_edge=?2 AND s.digest>?3) AND NOT EXISTS(SELECT 1 FROM artwork_compaction_failures f WHERE f.digest=s.digest) ORDER BY s.large_edge,s.digest LIMIT 1`, policy.edge, s.compactionCursorEdge, s.compactionCursor).Scan(&digest, &edge)
	if err == sql.ErrNoRows {
		s.compactionCursor, s.compactionCursorEdge = "", 0
		s.compactionDone = true
	}
	if err != nil {
		return "", err
	}
	s.compactionCursor, s.compactionCursorEdge = digest, edge
	return digest, nil
}

func (s *Service) artworkCompactionHasBacklog() bool {
	s.compactionMu.Lock()
	defer s.compactionMu.Unlock()
	return !s.compactionDone && s.compactionCursor != ""
}
