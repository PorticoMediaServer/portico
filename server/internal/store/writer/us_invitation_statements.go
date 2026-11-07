package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usInvitationCreateWrite     stmt.ID = "us.invitation.create.write"
	usInvitationEditWrite       stmt.ID = "us.invitation.edit.write"
	usInvitationEditAccessWrite stmt.ID = "us.invitation.edit_access.write"
	usInvitationRevokeWrite     stmt.ID = "us.invitation.revoke.write"
)

// Source effects only; inert until root composes current authority, original
// owner/receipt classification, library existence, create's 200-live-invite cap,
// immutable result, ONE scoped event, receipt accounting and proved FULL COMMIT.
// No statement accepts an account or issues/reveals either capability.
// Edit/revoke bump revision, invalidating every short capability bound to the
// preceding revision without rewriting/deleting that separate retained row.
// An already revoked row may take a NEW operation/revision, as today's API does.
func usInvitationEffectDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	boolean := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Boolean} }
	notes := stmt.Param{Name: "notes", Type: stmt.Text, Nullable: true}
	accessParams := []stmt.Param{text("mode"), integer("streams"), boolean("allow_remote"), boolean("access_tokens"), text("rating")}
	pk := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitations_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitations", Name: "sqlite_autoindex_access_invitations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	const result = ` RETURNING id,revision,state,expires_ms`
	const editWhere = ` WHERE id=? AND state='pending' AND revision=? AND revision<9007199254740991
AND expires_ms>? AND ?>0 AND ?>? AND ?<=?`
	editTail := []stmt.Param{text("id"), integer("expected_revision"), integer("now"), integer("now_positive"), integer("new_expiry"), integer("now_lower"), integer("new_expiry_upper"), integer("maximum_expiry")}
	editHead := []stmt.Param{notes, text("role"), text("libraries"), integer("expires")}
	defs := []stmt.Definition{
		{ID: usInvitationCreateWrite, SQL: `INSERT INTO access_invitations(id,notes,code_hash,role,allowed_libraries,state,created_ms,expires_ms,created_by,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)` + result,
			Params:  append([]stmt.Param{text("id"), notes, text("code_hash"), text("role"), text("libraries"), text("state"), integer("created"), integer("expires"), text("created_by"), integer("revision"), text("bound_username")}, accessParams...),
			Cohorts: []stmt.Cohort{{Name: "create", Bindings: []any{"us-invite-new", nil, usInvitationFixtureHash, "member", "[]", "pending", int64(1000), int64(604801000), "us-owner", int64(1), "", "inherit", int64(0), true, true, ""}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{ID: usInvitationEditWrite, SQL: `UPDATE access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 SET notes=?,role=?,allowed_libraries=?,expires_ms=?,revision=revision+1` + editWhere + result,
			Params:  append(append([]stmt.Param(nil), editHead...), editTail...),
			Cohorts: []stmt.Cohort{{Name: "preserve_access", Bindings: []any{nil, "member", "[]", int64(3000), "us-invite", int64(1), int64(1000), int64(1000), int64(3000), int64(1000), int64(3000), int64(2592001000)}}}, Plan: pk,
		},
		{ID: usInvitationEditAccessWrite, SQL: `UPDATE access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 SET notes=?,role=?,allowed_libraries=?,expires_ms=?,max_streams_mode=?,max_streams=?,allow_remote=?,access_tokens=?,max_content_rating=?,revision=revision+1` + editWhere + result,
			Params:  append(append(append([]stmt.Param(nil), editHead...), accessParams...), editTail...),
			Cohorts: []stmt.Cohort{{Name: "replace_access", Bindings: []any{nil, "admin", "[]", int64(3000), "limit", int64(2), false, false, "PG-13", "us-invite", int64(1), int64(1000), int64(1000), int64(3000), int64(1000), int64(3000), int64(2592001000)}}}, Plan: pk,
		},
		{ID: usInvitationRevokeWrite, SQL: `UPDATE access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 SET state='revoked',revision=revision+1
WHERE id=? AND state<>'accepted' AND revision=? AND revision<9007199254740991` + result,
			Params:  []stmt.Param{text("id"), integer("expected_revision")},
			Cohorts: []stmt.Cohort{{Name: "pending", Bindings: []any{"us-invite", int64(1)}}, {Name: "stale", Bindings: []any{"us-invite", int64(2)}}}, Plan: pk,
		},
	}
	for n := range defs {
		defs[n].Kind, defs[n].Owner = stmt.Write, "store/writer US invitations"
		defs[n].CallSites = []string{"store/writer/us_invitation_statements.go"}
	}
	return defs
}

// Fixed nonsecret fixture digest for Capture cohorts, not a capability.
const usInvitationFixtureHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// Root supplies trusted same-turn time, never a client timestamp. These are
// bindings only, with no executor, COMMIT or authority side effects.
func (p *usInvitationPrepared) effectBindings(nowMS int64) (stmt.ID, []any, error) {
	if p == nil || nowMS <= 0 || nowMS > usMaxRevision-usInvitationMaxSpanMS {
		return "", nil, ErrInvalid
	}
	var notes any
	if p.notes.Valid {
		notes = p.notes.String
	}
	access := []any{string(p.access.mode), int64(p.access.streams), p.access.allowRemote, p.access.accessTokens, p.access.rating}
	switch p.kind {
	case usInvitationCreate:
		if p.hours < 1 || p.hours > usInvitationMaxHours {
			return "", nil, ErrInvalid
		}
		args := []any{p.id, notes, p.codeHash, p.role, p.librariesJSON, "pending", nowMS, nowMS + int64(p.hours)*usInvitationHourMS, p.scope.issuer.account, int64(1), p.boundUsername}
		return usInvitationCreateWrite, append(args, access...), nil
	case usInvitationEdit:
		args := []any{notes, p.role, p.librariesJSON, p.expiresMS}
		id := usInvitationEditWrite
		if p.replaceAccess {
			args = append(args, access...)
			id = usInvitationEditAccessWrite
		}
		return id, append(args, p.id, p.expectedRevision, nowMS, nowMS, p.expiresMS, nowMS, p.expiresMS, nowMS+usInvitationMaxSpanMS), nil
	case usInvitationRevoke:
		return usInvitationRevokeWrite, []any{p.id, p.expectedRevision}, nil
	default:
		return "", nil, ErrInvalid
	}
}
