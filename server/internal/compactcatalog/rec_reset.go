package compactcatalog

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// ResetRecProfile forgets what a profile's recommendations have learned,
// inside the caller's transaction. The caller clears the profile's own
// feedback first (not interested), so the jobs that write queues are part of
// what this settles.
//
// Taste goes to nothing, but what the profile has done stays true: a watched,
// started, saved or rated title is still engaged, and a disliked one is still
// hidden, so neither comes back as a recommendation. Each stored signal keeps
// those flags with no weight, and every pending job is settled the same way
// (its flags from the facts as they stand, no weight) instead of being
// processed later, when it would teach the reset profile its old taste. A
// signal's weight returns only when its work is touched again: the next job
// on it sees the whole of its facts, which is what the profile does next.
//
// The profile's revision moves forward rather than being deleted, so a
// ranking memoised before the reset can never match a key read after it
// (deleted, it would count up from 1 again through keys already used).
func ResetRecProfile(ctx context.Context, tx *sql.Tx, profile string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM rec_profile_taste WHERE profile_id=?`, profile); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rec_profile_signals SET weight=0,long=0,short=0 WHERE profile_id=? AND (weight<>0 OR long<>0 OR short<>0)`, profile); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT work_id FROM rec_profile_jobs WHERE profile_id=?`, profile)
	if err != nil {
		return err
	}
	var works []int64
	for rows.Next() {
		var work int64
		if err = rows.Scan(&work); err != nil {
			rows.Close()
			return err
		}
		works = append(works, work)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, work := range works {
		next, err := RecSignalFor(ctx, tx, profile, work, now)
		if err != nil {
			return err
		}
		if !next.Engaged && !next.Hidden && !next.Finished {
			_, err = tx.ExecContext(ctx, `DELETE FROM rec_profile_signals WHERE profile_id=? AND work_id=?`, profile, work)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO rec_profile_signals(profile_id,work_id,weight,engaged,hidden,finished,at,long,short) VALUES(?,?,0,?,?,?,?,0,0)
			 ON CONFLICT(profile_id,work_id) DO UPDATE SET weight=0,engaged=excluded.engaged,hidden=excluded.hidden,finished=excluded.finished,at=excluded.at,long=0,short=0`,
				profile, work, b2i(next.Engaged), b2i(next.Hidden), b2i(next.Finished), recDaysText(next.At))
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM rec_profile_jobs WHERE profile_id=?`, profile); err != nil {
		return err
	}
	// A reader takes a profile's signals only once it has an epoch, so the
	// settled flags need one even when the profile had no taste yet.
	_, err = tx.ExecContext(ctx, `INSERT INTO rec_profile_revisions(profile_id,revision,epoch) VALUES(?,1,?) ON CONFLICT(profile_id) DO UPDATE SET revision=revision+1`, profile, math.Floor(UnixDays(now)))
	return err
}
