package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Named outcomes for a point invitation source miss. The 550-552 source CAS
// statements return zero rows for every cause; the dispatcher first reads the
// invitation row's role, state, revision, creator and expiry in the SAME frame
// and turns each cause into one of these errors before any effect.
var (
	ErrInvitationNotFound = errors.New("writer: invitation not found")
	ErrInvitationRevision = errors.New("writer: invitation revision conflict")
	ErrInvitationExpired  = errors.New("writer: invitation expired")
	ErrInvitationAccepted = errors.New("writer: invitation already accepted")
	ErrInvitationRevoked  = errors.New("writer: invitation already revoked")
)

// usInvitationSourceRow is the complete same-frame invitation source snapshot.
// It is a comparison/capture fact, never authority: the manager's Recheck owns
// current authority and ManagesTier owns the tier decision.
type usInvitationSourceRow struct {
	exists           bool
	id               string
	notes            sql.NullString
	codeHash         string
	role             string
	librariesJSON    string
	state            string
	createdMS        int64
	expiresMS        int64
	acceptedMS       sql.NullInt64
	createdBy        string
	accountID        sql.NullString
	revision         int64
	boundUsername    string
	maxStreamsMode   string
	maxStreams       int64
	allowRemote      bool
	accessTokens     bool
	maxContentRating string
}

const (
	usInvitationSourceRead   stmt.ID = "us.invitation.source.read"
	usInvitationSourceOctets stmt.ID = "us.invitation.source.octets"
	usInvitationExistsID     stmt.ID = "us.invitation.exists.id"
	usInvitationExistsCode   stmt.ID = "us.invitation.exists.code_hash"
	usInvitationLiveCount    stmt.ID = "us.invitation.live_count"
	usInvitationLibraryRead  stmt.ID = "us.invitation.library_exists"
)

// usInvitationSourceDefinitions is the dispatcher's finite G2 source-read set.
// None of these accepts a command, opens a transaction or grants authority.
func usInvitationSourceDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	pkPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitations_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitations", Name: "sqlite_autoindex_access_invitations_1", Origin: "pk",
			Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	codePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitations_2"}, Seeks: []string{"code_hash=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitations", Name: "sqlite_autoindex_access_invitations_2", Origin: "u",
			Columns: []stmt.IndexColumn{{Name: "code_hash", Collation: "BINARY"}}}}}
	defs := []stmt.Definition{
		{ID: usInvitationSourceRead, SQL: `SELECT id,notes,code_hash,role,allowed_libraries,state,created_ms,expires_ms,accepted_ms,created_by,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating
FROM access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 WHERE id=?`,
			Params:  []stmt.Param{text("id")},
			Cohorts: []stmt.Cohort{{Name: "pending", Bindings: []any{"us-invite"}}}, Plan: pkPlan},
		{ID: usInvitationSourceOctets, SQL: `SELECT octet_length(code_hash),octet_length(role),octet_length(allowed_libraries),octet_length(state),octet_length(created_by),coalesce(octet_length(account_id),0),octet_length(bound_username),octet_length(max_streams_mode),octet_length(max_content_rating),coalesce(octet_length(notes),0)
FROM access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 WHERE id=?`,
			Params:  []stmt.Param{text("id")},
			Cohorts: []stmt.Cohort{{Name: "pending", Bindings: []any{"us-invite"}}}, Plan: pkPlan},
		{ID: usInvitationExistsID, SQL: `SELECT 1 FROM access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 WHERE id=? LIMIT 2`,
			Params:  []stmt.Param{text("id")},
			Cohorts: []stmt.Cohort{{Name: "pending", Bindings: []any{"us-invite"}}}, Plan: pkPlan},
		{ID: usInvitationExistsCode, SQL: `SELECT 1 FROM access_invitations INDEXED BY sqlite_autoindex_access_invitations_2 WHERE code_hash=? LIMIT 2`,
			Params:  []stmt.Param{text("code_hash")},
			Cohorts: []stmt.Cohort{{Name: "nonsecret", Bindings: []any{usInvitationFixtureHash}}}, Plan: codePlan},
		{ID: usInvitationLiveCount, SQL: `SELECT count(*) FROM access_invitations INDEXED BY access_invitation_expiry WHERE state='pending' AND expires_ms>?`,
			Params:  []stmt.Param{integer("now")},
			Cohorts: []stmt.Cohort{{Name: "now", Bindings: []any{int64(1000)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"access_invitation_expiry"}, Seeks: []string{"state=? AND expires_ms>?"}}},
		{ID: usInvitationLibraryRead, SQL: `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`,
			Params:  []stmt.Param{text("library")},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"movies"}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"libraries"}, Seeks: []string{"id=?"}}},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US invitation source"
		defs[n].CallSites = []string{"store/writer/us_invitation_snapshot.go"}
	}
	return defs
}

func (r usInvitationSourceRow) access() usInvitationAccess {
	return usInvitationAccess{mode: r.maxStreamsMode, streams: int(r.maxStreams), allowRemote: r.allowRemote,
		accessTokens: r.accessTokens, rating: r.maxContentRating}
}

// readUSInvitationSource reads the point row behind the primary key. It reads
// role, state, revision, created_by and expiry together so a later CAS miss can
// be classified instead of collapsing into the same zero rows.
func readUSInvitationSource(ctx context.Context, turn *commandTurn, id string) (out usInvitationSourceRow, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || !usIdentifier(id) {
		return out, ErrInvalid
	}
	octets, err := usInvitationRow(ctx, turn, usInvitationSourceOctets, id)
	if err != nil {
		return out, err
	}
	var sizes [10]int64
	if err = octets.Scan(&sizes[0], &sizes[1], &sizes[2], &sizes[3], &sizes[4], &sizes[5], &sizes[6], &sizes[7], &sizes[8], &sizes[9]); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	var total int64
	for _, n := range sizes {
		if n < 0 || n > maxCanonicalResultBytes-total {
			return out, &Refusal{Code: RefusalCapacity}
		}
		total += n
	}
	row, err := usInvitationRow(ctx, turn, usInvitationSourceRead, id)
	if err != nil {
		return out, err
	}
	var notes, account sql.NullString
	var accepted sql.NullInt64
	var remote, tokens int64
	if err = row.Scan(&out.id, &notes, &out.codeHash, &out.role, &out.librariesJSON, &out.state,
		&out.createdMS, &out.expiresMS, &accepted, &out.createdBy, &account, &out.revision, &out.boundUsername,
		&out.maxStreamsMode, &out.maxStreams, &remote, &tokens, &out.maxContentRating); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if out.id != id || int64(len(out.codeHash)) != sizes[0] || int64(len(out.role)) != sizes[1] ||
		int64(len(out.librariesJSON)) != sizes[2] || int64(len(out.state)) != sizes[3] || int64(len(out.createdBy)) != sizes[4] ||
		int64(len(account.String)) != sizes[5] || int64(len(out.boundUsername)) != sizes[6] ||
		int64(len(out.maxStreamsMode)) != sizes[7] || int64(len(out.maxContentRating)) != sizes[8] ||
		int64(len(notes.String)) != sizes[9] || !usHex(out.codeHash, 64) || (out.role != "member" && out.role != "admin") ||
		(out.state != "pending" && out.state != "accepted" && out.state != "revoked") ||
		!usIdentifier(out.createdBy) || out.revision < 1 || out.revision >= usMaxRevision ||
		out.createdMS < 0 || out.expiresMS < 0 || (remote != 0 && remote != 1) || (tokens != 0 && tokens != 1) ||
		(out.maxStreamsMode != "inherit" && out.maxStreamsMode != "unlimited" && out.maxStreamsMode != "limit") ||
		(out.maxStreamsMode != "limit" && out.maxStreams != 0) || (out.maxStreamsMode == "limit" && (out.maxStreams < 1 || out.maxStreams > 1000)) ||
		len(out.maxContentRating) > 32 {
		return out, ErrResolveRequired
	}
	out.exists = true
	out.notes, out.acceptedMS, out.accountID = notes, accepted, account
	out.allowRemote, out.accessTokens = remote == 1, tokens == 1
	return out, nil
}

// classifyUSInvitationSource maps a missing/stale source row to one named
// outcome. Revision is checked before state/expiry so a caller that named the
// wrong revision always learns that conflict, not a side effect of drift.
func classifyUSInvitationSource(p *usInvitationPrepared, row usInvitationSourceRow, nowMS int64) error {
	if p == nil || (p.kind != usInvitationEdit && p.kind != usInvitationRevoke) || nowMS <= 0 {
		return ErrInvalid
	}
	if !row.exists {
		return ErrInvitationNotFound
	}
	if row.revision != p.expectedRevision {
		return ErrInvitationRevision
	}
	if row.state == "accepted" {
		return ErrInvitationAccepted
	}
	switch p.kind {
	case usInvitationEdit:
		if row.expiresMS <= nowMS {
			return ErrInvitationExpired
		}
		if row.state != "pending" {
			return ErrInvitationRevoked
		}
	case usInvitationRevoke:
		if row.state != "pending" && row.state != "revoked" {
			return ErrInvitationRevoked
		}
	}
	return nil
}

func usInvitationRow(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if turn == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, invalid("us_invitation_statement")
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}

func usInvitationRows(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	if turn == nil {
		return nil, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return nil, invalid("us_invitation_statement")
	}
	return turn.query(ctx, step.statementStep, args...)
}
