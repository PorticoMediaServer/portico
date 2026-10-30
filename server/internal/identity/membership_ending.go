package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Called only after the existing owner/admin target/revision checks, within the
// transaction that disables or erases this active membership. Generic erasure,
// account-initiated Departure, Start over and unused invitation expiry do not
// manufacture this cause. An accepted ordinary invitation never expires access.
func (s *Service) recordOwnerMembershipEndingTx(ctx context.Context, tx *sql.Tx, account string) error {
	return s.recordMembershipEndingTx(ctx, tx, account, "owner_removed", true)
}

func (s *Service) recordMembershipEndingTx(ctx context.Context, tx *sql.Tx, account, cause string, activeOnly bool) error {
	var recipient, incarnation string
	var disabled bool
	err := tx.QueryRowContext(ctx, `SELECT l.hosted_account_id,m.incarnation,m.disabled FROM direct_memberships m JOIN account_portico_links l ON l.account_id=m.account_id WHERE m.account_id=?`, account).Scan(&recipient, &incarnation, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if activeOnly && disabled {
		return nil
	}
	// A generic erasure keeps an already recorded cause for this exact original
	// incarnation. A later incarnation instead gets its own neutral tombstone.
	if cause == "" {
		var original string
		err = tx.QueryRowContext(ctx, `SELECT incarnation FROM hosted_membership_endings WHERE hosted_account_id=?`, recipient).Scan(&original)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && original == incarnation {
			return nil
		}
	}
	var sequence int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?) RETURNING sequence`, recipient).Scan(&sequence); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO hosted_membership_endings(hosted_account_id,incarnation,sequence,cause,ended_ms,expires_ms) VALUES(?,?,?,?,?,?) ON CONFLICT(hosted_account_id) DO UPDATE SET incarnation=excluded.incarnation,sequence=excluded.sequence,cause=excluded.cause,ended_ms=excluded.ended_ms,expires_ms=excluded.expires_ms WHERE excluded.sequence>hosted_membership_endings.sequence`, recipient, incarnation, sequence, cause, now, now+30*24*60*60*1000)
	return err
}
