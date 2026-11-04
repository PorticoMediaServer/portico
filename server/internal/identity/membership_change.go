package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

type membershipAuthority struct {
	role, libraries string
	disabled        bool
	revision        int64
}

func membershipAuthorityTx(ctx context.Context, tx *sql.Tx, account string) (out membershipAuthority, err error) {
	err = tx.QueryRowContext(ctx, `SELECT role,allowed_libraries,disabled,revision FROM direct_memberships WHERE account_id=?`, account).Scan(&out.role, &out.libraries, &out.disabled, &out.revision)
	return
}

func membershipLibrarySet(raw string) ([]string, error) {
	var libraries []string
	if err := json.Unmarshal([]byte(raw), &libraries); err != nil {
		return nil, err
	}
	slices.Sort(libraries)
	return slices.Compact(libraries), nil
}

// ExecMembershipTx is the shared mutation seam for one account's membership.
// It preserves the statement's result and publishes only an actual authority
// change, in this same transaction. Metadata and revision-only writes are quiet.
// The source census covers every production writer, across all build tags.
func ExecMembershipTx(ctx context.Context, tx *sql.Tx, account, statement string, args ...any) (sql.Result, error) {
	before, err := membershipAuthorityTx(ctx, tx, account)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return result, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return result, err
	}
	after, err := membershipAuthorityTx(ctx, tx, account)
	if err != nil {
		return result, err
	}
	oldLibraries, err := membershipLibrarySet(before.libraries)
	if err != nil {
		return result, err
	}
	newLibraries, err := membershipLibrarySet(after.libraries)
	if err != nil {
		return result, err
	}
	if before.role == after.role && before.disabled == after.disabled && slices.Equal(oldLibraries, newLibraries) {
		return result, nil
	}
	return result, apievents.Append(tx, apievents.AuthorityAudience("local", account), "authority.changed", "account", "self", strconv.FormatInt(after.revision, 10), nil)
}
