package identity

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PruneSessionHistory removes expired encrypted replay payloads, retired
// access tokens, lapsed PIN debt and unused PIN lineages in bounded,
// lowest-priority writes. Refresh predecessor hashes
// remain while a family is live so reuse still revokes it after receipt expiry.
func (s *Service) PruneSessionHistory(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	staleDebt := time.Now().Add(-24 * time.Hour).UnixMilli()
	unusedLineage := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	tasks := []struct {
		pending string
		remove  string
		args    []any
	}{
		{
			`SELECT EXISTS(SELECT 1 FROM identity_refresh_receipts WHERE expires_at<=? LIMIT 1)`,
			`DELETE FROM identity_refresh_receipts WHERE predecessor_hash IN (SELECT predecessor_hash FROM identity_refresh_receipts WHERE expires_at<=? ORDER BY expires_at LIMIT 128)`,
			[]any{now},
		},
		{
			`SELECT EXISTS(SELECT 1 FROM authorization_family_tokens t WHERE t.retired=1 AND t.expires_at<=? AND NOT EXISTS(SELECT 1 FROM authorization_family_renewals r WHERE r.predecessor_hash=t.token_hash) LIMIT 1)`,
			`DELETE FROM authorization_family_tokens WHERE token_hash IN (SELECT t.token_hash FROM authorization_family_tokens t WHERE t.retired=1 AND t.expires_at<=? AND NOT EXISTS(SELECT 1 FROM authorization_family_renewals r WHERE r.predecessor_hash=t.token_hash) ORDER BY t.expires_at LIMIT 128)`,
			[]any{now},
		},
		{
			`SELECT EXISTS(SELECT 1 FROM authorization_session_families f INDEXED BY authorization_families_retention JOIN identity_refresh_predecessors p ON p.family_id=f.id WHERE f.revoked=1 LIMIT 1)
			 OR EXISTS(SELECT 1 FROM authorization_session_families f INDEXED BY authorization_families_horizon JOIN identity_refresh_predecessors p ON p.family_id=f.id WHERE f.authorization_horizon<=? LIMIT 1)`,
			`DELETE FROM identity_refresh_predecessors WHERE predecessor_hash IN (SELECT predecessor_hash FROM (
			 SELECT p.predecessor_hash FROM authorization_session_families f INDEXED BY authorization_families_retention JOIN identity_refresh_predecessors p ON p.family_id=f.id WHERE f.revoked=1
			 UNION ALL
			 SELECT p.predecessor_hash FROM authorization_session_families f INDEXED BY authorization_families_horizon JOIN identity_refresh_predecessors p ON p.family_id=f.id WHERE f.authorization_horizon<=?
			 ) LIMIT 128)`,
			[]any{now},
		},
		{
			// PIN debt older than a day no longer counts (SelectDirectProfile).
			`SELECT EXISTS(SELECT 1 FROM direct_profile_pin_attempts WHERE last_failed_ms<? LIMIT 1)`,
			`DELETE FROM direct_profile_pin_attempts WHERE rowid IN (SELECT rowid FROM direct_profile_pin_attempts WHERE last_failed_ms<? LIMIT 128)`,
			[]any{staleDebt},
		},
		{
			// A lineage no session continues and that owes nothing. A day's
			// margin outlives any Quick Connect approval that names it.
			`SELECT EXISTS(SELECT 1 FROM identity_pin_lineages l WHERE l.created_at<=? AND NOT EXISTS(SELECT 1 FROM identity_family_pin_lineages f WHERE f.lineage_id=l.id) AND NOT EXISTS(SELECT 1 FROM direct_profile_pin_attempts a WHERE a.lineage_id=l.id) LIMIT 1)`,
			`DELETE FROM identity_pin_lineages WHERE id IN (SELECT l.id FROM identity_pin_lineages l WHERE l.created_at<=? AND NOT EXISTS(SELECT 1 FROM identity_family_pin_lineages f WHERE f.lineage_id=l.id) AND NOT EXISTS(SELECT 1 FROM direct_profile_pin_attempts a WHERE a.lineage_id=l.id) LIMIT 128)`,
			[]any{unusedLineage},
		},
	}
	var total int64
	for _, task := range tasks {
		var due bool
		if err := s.db.QueryRowContext(ctx, task.pending, task.args...).Scan(&due); err != nil {
			return total, err
		}
		if !due {
			continue
		}
		result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, task.remove, task.args...)
		if err != nil {
			return total, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// EndedSessionRetention is the default retention. The normalized sign-in
// policy supplies the current value to the bounded maintenance consumer.
const EndedSessionRetention = 90 * 24 * time.Hour

// PruneEndedSessions removes the records of sessions that ended (were signed
// out, or lapsed unused) longer ago than the current retention, a bounded
// batch at a time. A live session is never touched, whatever its age: a
// device that keeps renewing keeps its session.
func (s *Service) PruneEndedSessions(ctx context.Context) (int64, error) {
	policy, e := readSignInPolicy(ctx, s.db)
	if e != nil {
		return 0, e
	}
	cutoff := time.Now().Add(-time.Duration(policy.Values.EndedSessionsDays) * 24 * time.Hour).UTC().Format(time.RFC3339)
	const ended = `SELECT id FROM authorization_session_families WHERE (revoked=1 AND revoked_at<>'' AND revoked_at<=?1) OR authorization_horizon<=?1 LIMIT 64`
	var due bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(`+ended+`)`, cutoff).Scan(&due); err != nil || !due {
		return 0, err
	}
	var removed int64
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		removed = 0
		policy, e := ReadSignInPolicyTx(ctx, tx)
		if e != nil {
			return e
		}
		cutoff = time.Now().Add(-time.Duration(policy.Values.EndedSessionsDays) * 24 * time.Hour).UTC().Format(time.RFC3339)
		rows, err := tx.QueryContext(ctx, ended, cutoff)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			// What refers to the session without following its deletion goes
			// first; the rest goes with the row.
			for _, statement := range []string{
				`DELETE FROM authorization_family_renewals WHERE family_id=?`,
				`DELETE FROM authorization_family_tokens WHERE family_id=?`,
				`DELETE FROM identity_device_families WHERE family_id=?`,
				`DELETE FROM authorization_session_families WHERE id=?`,
			} {
				if _, err = tx.ExecContext(ctx, statement, id); err != nil {
					return err
				}
			}
			removed++
		}
		return nil
	})
	return removed, err
}
