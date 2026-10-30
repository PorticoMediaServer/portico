package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

type missingArtwork struct {
	Target        RepairTarget
	Role, Subject string
}

// missingArtworkTarget is one hint whose catalogue entity resolved to a stored
// integer id, in the exact shape the repair UPDATE matches.
type missingArtworkTarget struct {
	Kind     string
	EntityID int64
	Role     string
	Subject  string
}

// Reads coalesce repair hints in a bounded queue; only the worker writes. A
// missing object never makes a response wait for the writer gate.
func (s *Service) queueMissingArtwork(t RepairTarget, role, subject string) {
	s.missingMu.Lock()
	if s.missingArtwork == nil {
		s.missingArtwork = make(map[missingArtwork]struct{})
	}
	if len(s.missingArtwork) < 1024 {
		s.missingArtwork[missingArtwork{t, role, subject}] = struct{}{}
	}
	s.missingMu.Unlock()
	s.missingWake.Wake()
}

// requeueMissingArtwork returns hints that were taken from the queue but not
// completed. The queue is bounded, so a hint may still be dropped by the same
// rule that bounds admission; it is never silently consumed by this step.
func (s *Service) requeueMissingArtwork(batch []missingArtwork) {
	for _, pending := range batch {
		s.queueMissingArtwork(pending.Target, pending.Role, pending.Subject)
	}
}

func (s *Service) RepairMissingArtworkStep(ctx context.Context) error {
	s.missingMu.Lock()
	var batch []missingArtwork
	for v := range s.missingArtwork {
		batch = append(batch, v)
		delete(s.missingArtwork, v)
		if len(batch) == 32 {
			break
		}
	}
	s.missingMu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	// Resolve the bounded public ids before taking the writer gate, then close
	// every read cursor. An entity that no longer exists is dropped exactly like
	// a completed repair. A failed resolve leaves all the work pending: nothing
	// has been written yet, so even the hints resolved before the failure are
	// requeued.
	targets := make([]missingArtworkTarget, 0, len(batch))
	seen := make(map[missingArtworkTarget]struct{}, len(batch))
	for _, v := range batch {
		entity, err := entityid.Resolve(ctx, s.db, v.Target.ID)
		if errors.Is(err, entityid.ErrNotFound) {
			// The target is gone; drop its hint like a completed repair would.
			continue
		}
		if err != nil {
			s.requeueMissingArtwork(batch)
			return err
		}
		target := missingArtworkTarget{Kind: v.Target.Kind, EntityID: entity, Role: v.Role, Subject: v.Subject}
		// Two public ids can resolve to the same surviving entity after a merge.
		// One tuple already matches every row the duplicates would.
		if _, duplicate := seen[target]; duplicate {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil
	}
	// One gated transaction for the whole bounded batch. During the cache-loss
	// incident that produces these hints the writer gate is the scarce resource,
	// and one statement resetting the matching jobs costs one acquisition where
	// a statement per hint cost up to 32. Every predicate of the previous
	// single-hint UPDATE is unchanged; the row-value list is only a shorter way
	// to spell the same conjunction.
	query, args := missingArtworkReset(targets)
	if err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}); err != nil {
		// The transaction rolled back as a whole, so no hint in the batch
		// completed. Requeue all of them rather than report partial success.
		s.requeueMissingArtwork(batch)
		return err
	}
	return nil
}

// missingArtworkReset builds the one statement that resets a bounded batch. The
// predicates are the ones the per-hint statement used: the exact job identity,
// only a completed job, and never a provider-generated selection.
func missingArtworkReset(targets []missingArtworkTarget) (string, []any) {
	var query strings.Builder
	query.WriteString(`UPDATE artwork_jobs SET status='pending',attempts=0,next_attempt='' WHERE (kind,entity_id,role,subject) IN (VALUES `)
	args := make([]any, 0, len(targets)*4)
	for i, target := range targets {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString("(?,?,?,?)")
		args = append(args, target.Kind, target.EntityID, target.Role, target.Subject)
	}
	query.WriteString(`) AND status='complete' AND provider<>'generated'`)
	return query.String(), args
}
