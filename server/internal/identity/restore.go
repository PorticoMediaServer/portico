package identity

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A backup is a plain copy of the database, and a restore puts an older copy
// back. Two things must not follow from that:
//
//   - A copy of a backup must not resume a human sign-in. The database holds, for every
//     signed-in device, what is needed to keep it signed in: the stored answer
//     to its last renewal (its current tokens), pending sign-in codes and their
//     answers, the first-run receipt. Anyone who could read a backup could use
//     them.
//   - A restore must not bring a session back to life. A device that was signed
//     out, a member who was turned off and a television that was blocked after
//     the backup was taken would all be signed in again, because the backup
//     remembers them as they were.
//
// Both are closed the same way, with no step for the owner. Every backup copy
// has its human sessions ended and their live secrets removed as it is written
// (ScrubBackupCopy), so a backup holds no sign-in at all. And when a restore
// has just been applied, everyone is signed out before the server answers a
// request (SignEveryoneOutAfterRestore), whatever the restored copy held: an
// older backup, one made by another build, or a copy made by hand.
//
// API keys and personal access tokens are the one exception, on purpose: what
// an owner has connected to the server keeps working across a restore, as it
// does across a password change. A key that was live when the backup was taken
// is live again after it is restored, including one revoked in between; the
// owner sees it in the key list and can revoke it again. Named integration
// app passwords are persistent service credentials too. Owner-recoverable keys
// and app passwords keep their recoverable storage in the local backup; personal
// access tokens remain digest-only. Account passwords and PINs remain one-way.
//
// People sign in again after a restore. Their accounts, profiles, passwords,
// second factors, devices and everything they saved are untouched; so is each
// device's approval. What is deliberately not removed is anything that is not
// a session: password hashes and second-factor secrets stay in the database
// and in its backups, in the clear, as the server stores them.

// sessionScrub is every statement that ends sessions and removes the secrets
// that could continue or start one, with the table each needs. A table this
// build does not have is skipped, so the list never stops a backup.
var sessionScrub = []struct{ table, statement string }{
	// Every signed-in device, account session and paired screen.
	{"authorization_session_families", `UPDATE authorization_session_families SET revoked=1,revoked_reason='restored',revoked_at=?1 WHERE revoked=0`},
	// The tokens of every session that is now ended. An API key or personal
	// access token is not a sign-in and is not ended here: it has a lifetime of
	// its own (api_credentials.go), the schema keeps its family from being
	// swept above, and so its one token stays. API keys and app passwords also
	// retain owner-recoverable secrets by product policy; local backups carry
	// those secrets. Human session renewal credentials are removed below.
	{"authorization_family_tokens", `UPDATE authorization_family_tokens SET retired=1 WHERE retired=0 AND family_id NOT IN(SELECT id FROM authorization_session_families WHERE revoked=0)`},
	// Renewal: the credentials themselves, and the stored answers that hold a
	// session's current tokens in the clear.
	{"identity_refresh_receipts", `DELETE FROM identity_refresh_receipts`},
	{"identity_refresh_predecessors", `DELETE FROM identity_refresh_predecessors`},
	{"identity_refresh_credentials", `DELETE FROM identity_refresh_credentials`},
	{"authorization_family_renewals", `DELETE FROM authorization_family_renewals`},
	// Sign-ins in progress: television codes and their sealed answers, second
	// factor challenges, Portico Account challenges, the first-run receipt.
	{"quick_connect_v1", `DELETE FROM quick_connect_v1`},
	{"identity_factor_challenges", `DELETE FROM identity_factor_challenges`},
	{"hosted_identity_challenges", `DELETE FROM hosted_identity_challenges`},
	{"onboarding_state_v1", `UPDATE onboarding_state_v1 SET receipt=NULL WHERE receipt IS NOT NULL`},
	// Things a device holds instead of entering a PIN or a password again.
	{"topshelf_tokens", `DELETE FROM topshelf_tokens`},
	// Screens paired as Cast targets renew with their own device token.
	{"social_cast_devices", `UPDATE social_cast_devices SET state='revoked' WHERE state IN('pending','active')`},
	{"social_cast_bootstraps", `UPDATE social_cast_bootstraps SET state='cancelled' WHERE state='pending'`},
}

// endEverySessionTx runs the scrub in tx. It reports how many sessions it
// ended and how many rows it changed in all.
func endEverySessionTx(ctx context.Context, tx *sql.Tx, now time.Time) (ended, changed int64, err error) {
	stamp := now.UTC().Format(time.RFC3339)
	var live int64
	for _, step := range sessionScrub {
		var present bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, step.table).Scan(&present); err != nil {
			return 0, 0, err
		}
		if !present {
			continue
		}
		var result sql.Result
		if step.table == "authorization_session_families" {
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&live); err != nil {
				return 0, 0, err
			}
			result, err = tx.ExecContext(ctx, step.statement, stamp)
		} else {
			result, err = tx.ExecContext(ctx, step.statement)
		}
		if err != nil {
			return 0, 0, err
		}
		n, _ := result.RowsAffected()
		changed += n
		if step.table == "authorization_session_families" {
			// Counted afterwards: the statement also touches the families the
			// schema keeps, and those did not end.
			var kept int64
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&kept); err != nil {
				return 0, 0, err
			}
			ended = live - kept
		}
	}
	if err = scrubMutationLifetimesTx(ctx, tx); err != nil {
		return 0, 0, err
	}
	return ended, changed, nil
}

// scrubMutationLifetimesTx retires the P251 lifetimes this copy carried, before
// the copy is served or handed back as a backup.
//
// A restore puts an older database in front of people, but it must not put their
// old mutation history back in front of them. Receipts and roots are removed
// first, so nothing committed by an earlier sign-in can be replayed; the profile
// and installation lifetimes are then rotated so the same textual profile id or
// installation secret is a different lifetime, and a request carrying either of
// the old ones cannot match. The profile rows themselves stay: an erased profile
// keeps its sanitized deleted row for history, and deleting it here would also
// delete the name a person's play history is shown under.
//
// A purge that was already promised is not superseded by the rotation, but only
// the exact original promise may be carried forward. Each profile is rotated one
// at a time in this same transaction: before its old lifetime is overwritten, the
// jobs for that exact profile whose profile incarnation AND membership
// incarnation match, and whose profile is the sanitized erased row, are moved to
// the new lifetime and their walk restarts from the first phase. A job that names
// a different lifetime, a different membership incarnation, or a profile that is
// still live is left exactly as it is: it is stale for the old lifetime, so it
// stays blocked rather than being rebound to a lifetime (or a profile) it never
// promised to purge. Nothing is inferred from a reused textual id.
//
// API credentials are not touched: an old receipt still cannot apply across a
// restore because the receipt is gone, not because the key is.
func scrubMutationLifetimesTx(ctx context.Context, tx *sql.Tx) error {
	present := func(table string) (bool, error) {
		var ok bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&ok)
		return ok, err
	}
	// Children before owners. The root release trigger keeps the shared P251
	// byte budget exact, exactly as live cleanup does.
	for _, table := range []string{"user_mutation_receipts", "user_mutation_roots"} {
		ok, err := present(table)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return err
		}
	}
	profiles, err := present("direct_profiles")
	if err != nil {
		return err
	}
	memberships, err := present("direct_memberships")
	if err != nil {
		return err
	}
	jobs, err := present("security_purge_jobs")
	if err != nil {
		return err
	}
	if profiles {
		if err = rotateProfileLifetimesTx(ctx, tx, jobs && memberships); err != nil {
			return err
		}
	}
	// The installation secret survives (the app still holds it) but its lifetime
	// does not: the incarnation is new and the registration watermark is cleared,
	// so no old registration tuple can replay. A retired lifetime is not
	// resurrected here.
	installations, err := present("identity_installations")
	if err != nil {
		return err
	}
	if installations {
		if _, err = tx.ExecContext(ctx, `UPDATE identity_installations SET incarnation=lower(hex(randomblob(16))),registration_revision=0,registration_nonce='',registration_root=''`); err != nil {
			return err
		}
	}
	return nil
}

// rotateProfileLifetimesTx walks the profiles by keyset and rotates one old
// lifetime to its new value at a time. Rebindable is decided against that
// profile's own old lifetime and membership incarnation, read in this same
// transaction before the update, so no job is ever matched by a textual id after
// its old lifetime has been replaced. Memory stays at one bounded page.
func rotateProfileLifetimesTx(ctx context.Context, tx *sql.Tx, rebindJobs bool) error {
	const page = 128
	lastAccount, lastProfile := "", ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT account_id,id,deleted,lifetime FROM direct_profiles WHERE account_id>? OR (account_id=? AND id>?) ORDER BY account_id,id LIMIT ?`, lastAccount, lastAccount, lastProfile, page)
		if err != nil {
			return err
		}
		// Read the whole bounded page before writing: a write must not run while
		// this transaction's read cursor is still open.
		type profileRow struct {
			account, profile, old string
			deleted               int
		}
		batch := make([]profileRow, 0, page)
		for rows.Next() {
			var row profileRow
			if err = rows.Scan(&row.account, &row.profile, &row.deleted, &row.old); err != nil {
				rows.Close()
				return err
			}
			lastAccount, lastProfile = row.account, row.profile
			batch = append(batch, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		account, incarnation := "", ""
		for _, row := range batch {
			if row.account != account {
				account, incarnation = row.account, ""
				if rebindJobs {
					if e := tx.QueryRowContext(ctx, `SELECT incarnation FROM direct_memberships WHERE account_id=?`, row.account).Scan(&incarnation); e != nil && !errors.Is(e, sql.ErrNoRows) {
						return e
					}
				}
			}
			next := newMutationIncarnation()
			// Only the sanitized erased row can still own a purge promise, and
			// only the job that named its exact old lifetime and current membership
			// incarnation is that promise. Everything else stays stale.
			if rebindJobs && row.deleted != 0 && incarnation != "" {
				if _, err = tx.ExecContext(ctx, `UPDATE security_purge_jobs SET profile_incarnation=?,phase=0,cursor_key='',process_epoch='',attempt=0,next_attempt_ms=0,state='pending' WHERE authority='local' AND account_id=? AND profile_id=? AND profile_incarnation=? AND account_incarnation=?`, next, row.account, row.profile, row.old, incarnation); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE direct_profiles SET lifetime=? WHERE account_id=? AND id=?`, next, row.account, row.profile); err != nil {
				return err
			}
		}
		if len(batch) < page {
			return nil
		}
	}
}

// ScrubBackupCopy ends every session in a finished backup copy of the database
// and removes the live secrets from it, before the copy becomes a backup. It
// works on the copy's private candidate handle, never the running server's
// database: it takes no write-gate ticket and has no live effect. The backup
// service calls it on each copy it writes; it is safe to run again on a copy
// that was already scrubbed. The caller closes the candidate, which folds the
// copy's write-ahead log back into the file.
func ScrubBackupCopy(ctx context.Context, copy *dbwork.Candidate) error {
	if copy == nil {
		return errors.New("no backup copy to scrub")
	}
	return copy.WithTx(ctx, func(tx *sql.Tx) error {
		// Deleted rows are overwritten, not just unlinked, so the secrets do
		// not linger in the copy's free pages for a reader of the file to
		// find. This costs a write per freed page, not a rewrite of the
		// database. The setting belongs to a connection, so it is made on the
		// one that does the deleting, and checked.
		var secure int
		if err := tx.QueryRowContext(ctx, `PRAGMA secure_delete=ON`).Scan(&secure); err != nil {
			return err
		}
		if secure != 1 {
			return errors.New("the backup copy cannot be scrubbed: deleted content would stay readable")
		}
		_, _, err := endEverySessionTx(ctx, tx, time.Now())
		return err
	})
}

const restoreHandledKey = "identity.restoreHandled"

// SignEveryoneOutAfterRestore signs everyone out once for each restore that
// was applied. restore identifies the restore (the time its outcome was
// recorded); the database remembers the last one it handled, and a restored
// database remembers an older one or none, so the first start after a restore
// always acts and later starts do not. It runs before the server accepts
// requests, and reports whether it acted.
func (s *Service) SignEveryoneOutAfterRestore(ctx context.Context, restore string) (bool, error) {
	if restore == "" {
		return false, nil
	}
	acted := false
	var ended int64
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		acted, ended = false, 0
		var handled string
		err := tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, restoreHandledKey).Scan(&handled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if handled == restore {
			return nil
		}
		if ended, _, err = endEverySessionTx(ctx, tx, time.Now()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, restoreHandledKey, restore); err != nil {
			return err
		}
		acted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if acted {
		log.Printf("This server was restored from a backup. Everyone has been signed out (%d sessions ended); accounts, passwords and devices are unchanged, and people sign in again.", ended)
	}
	return acted, nil
}
