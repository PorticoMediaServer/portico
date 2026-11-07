package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usInvitationShortSourceOctets stmt.ID = "us.invitation.short.source.octets"
const usInvitationShortSourceOctetsSQL = `SELECT octet_length(s.invitation_id),octet_length(s.lookup_hash),octet_length(s.binding_hash),octet_length(s.server_id),octet_length(s.sign_in_kind),octet_length(s.purpose) FROM access_invitation_short_codes AS s INDEXED BY sqlite_autoindex_access_invitation_short_codes_1 WHERE s.invitation_id=?`

func usInvitationShortSourceDefinitions() []stmt.Definition {
	return []stmt.Definition{{ID: usInvitationShortSourceOctets, SQL: usInvitationShortSourceOctetsSQL, Kind: stmt.Read, Owner: "store/writer US short invitation source", CallSites: []string{"store/writer/us_invitation_short_snapshot.go"}, Params: []stmt.Param{{Name: "invitation", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"invite"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitation_short_codes_1"}, Seeks: []string{"invitation_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitation_short_codes", Name: "sqlite_autoindex_access_invitation_short_codes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "invitation_id", Collation: "BINARY"}}}}}}}
}

// Fresh-only actual replacement footprint. Source SQL permits an unbounded
// server_id; do not substitute today's generated server-ID width for old bytes.
func readUSInvitationShortReplacementBytes(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, invitation string) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return 0, err
	}
	if !usIdentifier(invitation) {
		return 0, ErrInvalid
	}
	row, err := usPolicyRow(ctx, turn, usInvitationShortSourceOctets, invitation)
	if err != nil {
		return 0, err
	}
	var n [6]int64
	if err = row.Scan(&n[0], &n[1], &n[2], &n[3], &n[4], &n[5]); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	for _, bytes := range n {
		if bytes < 0 || bytes > math.MaxInt64-out {
			return 0, ErrResolveRequired
		}
		out += bytes
	}
	if err = a.requireMemory(ctx, turn, out); err != nil {
		return 0, err
	}
	return out, turn.checkLifetimes(ctx)
}
