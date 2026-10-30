package hosted

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Full reconciliation retains explicit inactive tombstones as well as active
// standings. Otherwise a lost delta would erase the cause before delivery.
// Sequence is bounded by the same publication's Through, never a later event.
func attachMembershipEndings(ctx context.Context, db *sql.DB, members []MemberStanding, through int64) error {
	var ids []string
	for _, member := range members {
		if !member.Active {
			ids = append(ids, member.AccountID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	args := []any{through, time.Now().UnixMilli()}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, `SELECT hosted_account_id,incarnation,sequence,cause,ended_ms,expires_ms FROM hosted_membership_endings WHERE sequence<=? AND expires_ms>? AND hosted_account_id IN(?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var account, incarnation string
		var ended, expires int64
		var event MembershipEnding
		if err = rows.Scan(&account, &incarnation, &event.Sequence, &event.Cause, &ended, &expires); err != nil {
			return err
		}
		event.EndedAt, event.ExpiresAt = time.UnixMilli(ended).UTC(), time.UnixMilli(expires).UTC()
		for i := range members {
			member := &members[i]
			if member.AccountID == account && !member.Active && (member.MembershipID == "" || member.MembershipID == incarnation) {
				member.MembershipID = incarnation
				if event.Cause == "owner_removed" {
					member.Ending = &event
				}
				break
			}
		}
	}
	return rows.Err()
}
