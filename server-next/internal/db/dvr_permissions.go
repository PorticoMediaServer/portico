package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// DvrPermission reads the existing account ceiling and the profile's narrower switch.
type DvrPermission struct {
	AccountID, AccountName, ProfileID, ProfileName, Authority            string
	AccountAllowed, ProfileAllowed, LiveAllowed, CanRecord, OwnerAccount bool
	AccountRevision, Revision                                            int64
}

func (d *DB) DvrPermissions(ctx context.Context) ([]DvrPermission, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT u.public_id, u.username, p.public_id, p.name,
 CASE WHEN u.portico_account_id IS NULL THEN 'local' ELSE 'hosted' END,
 u.role = 'owner' OR u.allow_dvr, p.allow_dvr, u.allow_live_tv AND p.allow_live_tv,
 u.role = 'owner', u.revision, p.access_changed_at
 FROM users u JOIN profiles p ON p.user_id = u.id
 WHERE u.state = 'active' AND u.disabled_at IS NULL ORDER BY u.id, p.position, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DvrPermission{}
	for rows.Next() {
		var row DvrPermission
		if err := rows.Scan(&row.AccountID, &row.AccountName, &row.ProfileID, &row.ProfileName, &row.Authority, &row.AccountAllowed, &row.ProfileAllowed, &row.LiveAllowed, &row.OwnerAccount, &row.AccountRevision, &row.Revision); err != nil {
			return nil, err
		}
		row.CanRecord = row.ProfileAllowed && row.AccountAllowed && (row.OwnerAccount || row.LiveAllowed)
		out = append(out, row)
	}
	return out, rows.Err()
}

// SetDvrPermission changes one switch, never the account's other limits or another profile.
func (d *DB) SetDvrPermission(ctx context.Context, actorID int64, account, profile string, enabled bool, revision int64, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var userID int64
		var role string
		err := tx.QueryRow(`SELECT id, role FROM users WHERE public_id = ? AND state = 'active' AND disabled_at IS NULL`, account).Scan(&userID, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if profile == "" {
			if role == "owner" {
				return ErrNotPermitted
			}
			result, err := tx.Exec(`UPDATE users SET allow_dvr = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ?`, enabled, now.UnixMilli(), userID, revision)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return ErrStale
			}
			_, err = tx.Exec(`UPDATE profiles SET access_changed_at = max(access_changed_at + 1, ?), updated_at = ? WHERE user_id = ?`, now.UnixMilli(), now.UnixMilli(), userID)
			if err != nil {
				return err
			}
			return audit(tx, actorID, "dvr.permission", account, strconv.FormatBool(enabled), now)
		}
		result, err := tx.Exec(`UPDATE profiles SET allow_dvr = ?, access_changed_at = max(access_changed_at + 1, ?), updated_at = ? WHERE user_id = ? AND public_id = ? AND access_changed_at = ?`, enabled, now.UnixMilli(), now.UnixMilli(), userID, profile, revision)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrStale
		}
		return audit(tx, actorID, "dvr.permission", profile, strconv.FormatBool(enabled), now)
	})
}
