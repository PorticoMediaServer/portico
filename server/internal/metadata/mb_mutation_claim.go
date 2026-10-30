package metadata

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Only a transient key cursor, never a stored query/member result. The normal
// singleton provider loop advances past at most 128 excluded due jobs per
// turn, so a deleting artist's long prefix cannot monopolize a reader. All
// claims are still rechecked by PK under the writer. Losing this cursor on
// restart is harmless. A walk ends on exhaustion, a successful pick or an
// explicit manual intent, rather than a wall-clock TTL shorter than the worker's
// safety tick. Bound handles; no timer/worker.
type mbMutationClaimCursor struct {
	Next, Lease, Kind string
	Entity            int64
	Continue          bool
}

var mbMutationClaims = struct {
	sync.Mutex
	ByDB map[*sql.DB]mbMutationClaimCursor
}{ByDB: make(map[*sql.DB]mbMutationClaimCursor)}

func (s *Service) pickMBMutationSafe(ctx context.Context, stamp string) (kind, public string, err error) {
	mbMutationClaims.Lock()
	cursor := mbMutationClaims.ByDB[s.db]
	mbMutationClaims.Unlock()
	read := dbwork.ReadHandle(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db)
	rows, err := read.QueryContext(ctx, `WITH due AS MATERIALIZED (
 SELECT * FROM mb_jobs WHERE status='pending' AND next_attempt<=? AND lease_until<=?
 AND (next_attempt,lease_until,kind,entity_id)>(?,?,?,?)
 ORDER BY next_attempt,lease_until,kind,entity_id LIMIT 128)
 SELECT mb_jobs.kind,pid(e.public_id),mb_jobs.next_attempt,mb_jobs.lease_until,mb_jobs.entity_id,(`+mbClaimable+`) FROM due AS mb_jobs JOIN catalog_entities e ON e.id=mb_jobs.entity_id ORDER BY mb_jobs.next_attempt,mb_jobs.lease_until,mb_jobs.kind,mb_jobs.entity_id`, stamp, stamp, cursor.Next, cursor.Lease, cursor.Kind, cursor.Entity, stamp, stamp, stamp)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	examined := 0
	for rows.Next() {
		var k, p string
		var allowed bool
		if err = rows.Scan(&k, &p, &cursor.Next, &cursor.Lease, &cursor.Entity, &allowed); err != nil {
			return "", "", err
		}
		cursor.Kind = k
		examined++
		if allowed {
			kind, public = k, p
			break
		}
	}
	if err = rows.Err(); err != nil {
		return "", "", err
	}
	if examined < 128 || kind != "" {
		// A successful pick ends this excluded-prefix walk. Revisit the due
		// head next time so a manual selection of that same PK is immediate.
		cursor = mbMutationClaimCursor{}
	}
	cursor.Continue = examined == 128 && kind == ""
	mbMutationClaims.Lock()
	if len(mbMutationClaims.ByDB) >= 8 {
		if _, ok := mbMutationClaims.ByDB[s.db]; !ok {
			for key := range mbMutationClaims.ByDB {
				delete(mbMutationClaims.ByDB, key)
				break
			}
		}
	}
	mbMutationClaims.ByDB[s.db] = cursor
	mbMutationClaims.Unlock()
	return kind, public, nil
}

func (s *Service) resetMBMutationPick() {
	mbMutationClaims.Lock()
	delete(mbMutationClaims.ByDB, s.db)
	mbMutationClaims.Unlock()
}

// This is read-only cursor progress, not a database commit. The provider lane
// consumes it once so it schedules the next raw window without pretending that
// an idle pass changed catalog state. Exhaustion or a throttled next pass returns
// to the normal signal/safety schedule; excluded prefixes cannot create a loop.
func (s *Service) takeMBMutationPickContinuation() bool {
	mbMutationClaims.Lock()
	defer mbMutationClaims.Unlock()
	cursor := mbMutationClaims.ByDB[s.db]
	more := cursor.Continue
	cursor.Continue = false
	if _, exists := mbMutationClaims.ByDB[s.db]; exists {
		mbMutationClaims.ByDB[s.db] = cursor
	}
	return more
}

// One due-time hint follows actual persisted job backoff through the existing
// provider worker's timer. It is not a job/result cache or authority. Restart
// may lose the hint; the ordinary safety wake still reads the durable job.
func (s *Service) noteMBAdoptionRetry(at time.Time) {
	deadline, now := at.UnixNano(), s.publicationTime().UnixNano()
	for {
		old := s.mbRetryDue.Load()
		if old > now && old <= deadline {
			return
		}
		if s.mbRetryDue.CompareAndSwap(old, deadline) {
			return
		}
	}
}
func (s *Service) mbAdoptionRetryDelay() time.Duration {
	for {
		deadline := s.mbRetryDue.Load()
		if deadline == 0 {
			return 0
		}
		delay := time.Duration(deadline - s.publicationTime().UnixNano())
		if delay > 0 {
			return delay
		}
		if s.mbRetryDue.CompareAndSwap(deadline, 0) {
			return 0
		}
	}
}
