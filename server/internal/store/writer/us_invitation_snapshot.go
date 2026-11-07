package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usInvitationSourceOctets   stmt.ID = "us.invitation.source.octets"
	usInvitationSourceFacts    stmt.ID = "us.invitation.source.facts"
	usInvitationLiveCapacity   stmt.ID = "us.invitation.live.capacity"
	usInvitationLibraryPresent stmt.ID = "us.invitation.library.present"
)
const usInvitationSourceOctetsSQL = `SELECT octet_length(i.id),coalesce(octet_length(i.notes),0),octet_length(i.code_hash),octet_length(i.role),octet_length(i.allowed_libraries),octet_length(i.state),coalesce(octet_length(i.account_id),0),octet_length(i.bound_username),octet_length(i.max_streams_mode),octet_length(i.max_content_rating) FROM access_invitations AS i INDEXED BY sqlite_autoindex_access_invitations_1 WHERE i.id=? AND i.revision=?`

// Only source snapshots/preconditions. Parent must classify the original
// immutable receipt BEFORE fresh-only target, library or capacity checks.
func usInvitationSourceDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	point := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitations_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitations", Name: "sqlite_autoindex_access_invitations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	params := []stmt.Param{text("invitation"), integer("revision")}
	cohort := []stmt.Cohort{{Name: "point", Bindings: []any{"us-invitation", int64(1)}}}
	defs := []stmt.Definition{
		{ID: usInvitationSourceOctets, SQL: usInvitationSourceOctetsSQL, Params: params, Cohorts: cohort, Plan: point},
		{ID: usInvitationSourceFacts, SQL: `SELECT i.id,i.notes,i.code_hash,i.role,i.allowed_libraries,i.state,i.created_ms,i.expires_ms,i.accepted_ms,i.account_id,i.revision,i.bound_username,i.max_streams_mode,i.max_streams,i.allow_remote,i.access_tokens,i.max_content_rating FROM access_invitations AS i INDEXED BY sqlite_autoindex_access_invitations_1 WHERE i.id=? AND i.revision=?`, Params: params, Cohorts: cohort, Plan: point},
		// Exact existing 200-live-invitation product admission; numeric rows only.
		// Equality-prefix+expiry range stops at 200, without COUNT or audience work.
		{ID: usInvitationLiveCapacity, SQL: `SELECT 1 FROM access_invitations INDEXED BY access_invitation_expiry WHERE state='pending' AND expires_ms>? ORDER BY expires_ms,id LIMIT 200`, Params: []stmt.Param{integer("now")}, Cohorts: []stmt.Cohort{{Name: "live", Bindings: []any{int64(1000)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"access_invitation_expiry"}, Seeks: []string{"state=? AND expires_ms>?"}}},
		{ID: usInvitationLibraryPresent, SQL: `SELECT 1 FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?`, Params: []stmt.Param{text("library")}, Cohorts: []stmt.Cohort{{Name: "library", Bindings: []any{"library"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},
	}
	for j := range defs {
		defs[j].Kind = stmt.Read
		defs[j].Owner = "store/writer US invitation source"
		defs[j].CallSites = []string{"store/writer/us_invitation_snapshot.go"}
	}
	return defs
}

// Reads only the ACTUAL source bytes after an exact closed numeric preflight in
// the same transaction. Kind/owner/applied time/public link-server/Code are
// assigned later from independently proved scope and actual transition facts.
func readUSInvitationSource(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, id string, revision int64) (out usInvitationSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	if !usIdentifier(id) || revision < 1 || revision > usMaxRevision {
		return out, ErrInvalid
	}
	row, err := usPolicyRow(ctx, turn, usInvitationSourceOctets, id, revision)
	if err != nil {
		return out, err
	}
	var lengths [10]int64
	dest := make([]any, len(lengths))
	for j := range lengths {
		dest[j] = &lengths[j]
	}
	if err = row.Scan(dest...); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	var bytes int64
	for _, n := range lengths {
		if n < 0 || n > math.MaxInt64-bytes {
			return out, ErrResolveRequired
		}
		bytes += n
	}
	if err = a.requireMemory(ctx, turn, bytes); err != nil {
		return out, err
	}
	row, err = usPolicyRow(ctx, turn, usInvitationSourceFacts, id, revision)
	if err != nil {
		return out, err
	}
	var raw string
	if err = row.Scan(&out.id, &out.notes, &out.codeHash, &out.role, &raw, &out.state, &out.createdMS, &out.expiresMS, &out.acceptedMS, &out.accountID, &out.revision, &out.boundUsername, &out.access.mode, &out.access.streams, &out.access.allowRemote, &out.access.accessTokens, &out.access.rating); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out.id != id || out.revision != revision || !usHex(out.codeHash, 64) {
		return out, ErrResolveRequired
	}
	out.libraries, err = decodeUSLibraryIDs(raw)
	if err != nil || len(out.libraries) > usInvitationMaxLibraries {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if out.libraries == nil {
		out.libraries = []string{}
	}
	out.count = len(out.libraries)
	return out, turn.checkLifetimes(ctx)
}

func checkUSInvitationFreshLibraries(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, ids []string) (err error) {
	defer func() {
		_, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, errors.Join(err, a.check(turn)))
	}()
	if err = a.check(turn); err != nil {
		return err
	}
	if len(ids) > usInvitationMaxLibraries {
		return ErrInvalid
	}
	for _, id := range ids {
		if !usIdentifier(id) {
			return ErrInvalid
		}
		row, e := usPolicyRow(ctx, turn, usInvitationLibraryPresent, id)
		if e != nil {
			return e
		}
		var one int64
		if e = row.Scan(&one); errors.Is(e, sql.ErrNoRows) {
			return invalid("allowedLibraries")
		} else if e != nil {
			return e
		}
		if one != 1 {
			return ErrResolveRequired
		}
	}
	return turn.checkLifetimes(ctx)
}
func checkUSInvitationFreshCapacity(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, now int64) (err error) {
	defer func() {
		_, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, errors.Join(err, a.check(turn)))
	}()
	if err = a.check(turn); err != nil {
		return err
	}
	if now <= 0 {
		return ErrInvalid
	}
	rows, err := usPolicyRows(ctx, turn, usInvitationLiveCapacity, now)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	count := 0
	for rows.Next() {
		var one int64
		if err = rows.Scan(&one); err != nil {
			return err
		}
		if one != 1 {
			return ErrResolveRequired
		}
		count++
		if count == 200 {
			return &Refusal{Code: RefusalCapacity}
		}
	}
	return errors.Join(rows.Err(), turn.checkLifetimes(ctx))
}
