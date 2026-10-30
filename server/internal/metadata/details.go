package metadata

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// QueueRefresh asks the screen worker to look at one film, show or episode again. The screen
// worker owns film matching and details; the retired legacy queues are not
// written.
var ErrRefreshKind = errors.New("metadata refresh is not available for this item kind")

func (s *Service) QueueRefresh(item string) error {
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = s.QueueRefreshTx(context.Background(), tx, item); e != nil {
		return e
	}
	return gated.Commit()
}

// QueueRefreshTx participates in the durable job's progress transaction.
func (s *Service) QueueRefreshTx(ctx context.Context, tx *sql.Tx, item string) error {
	var e error
	entity, e := resolveEntity(ctx, tx, item)
	if e != nil {
		return e
	}
	var targetKind string
	if e = tx.QueryRowContext(ctx, `SELECT target_kind FROM screen_metadata_work WHERE target_id=?`, entity).Scan(&targetKind); errors.Is(e, sql.ErrNoRows) {
		return ErrRefreshKind
	} else if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',force_refresh=1,generation=generation+1,revision=revision+1,requested_provider='',query_override='',lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, targetKind, entity); e != nil {
		return e
	}
	// A person asking for a refresh is the one thing the freshness rule must never stand in
	// the way of: the rule exists to stop this server asking on its own. Forgetting what this
	// item's accepted providers were last told makes the next pass go and look.
	if _, e = tx.ExecContext(ctx, `DELETE FROM metadata_document_freshness WHERE EXISTS(SELECT 1 FROM screen_metadata_publications p WHERE p.target_id=? AND p.decision='accepted' AND p.provider=metadata_document_freshness.provider AND p.provider_type=metadata_document_freshness.kind AND (metadata_document_freshness.resource=p.provider_id OR metadata_document_freshness.resource LIKE p.provider_id||'@%'))`, entity); e != nil {
		return e
	}

	return nil
}
