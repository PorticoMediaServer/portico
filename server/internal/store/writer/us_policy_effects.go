package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usMemberPolicyWrite  stmt.ID = "us.policy.member.write"
	usProfilePolicyWrite stmt.ID = "us.policy.profile.write"
)

// Same-turn W composition must first authenticate the current issuer, classify
// the original receipt, admit memory from the octet preflight, load target
// facts, and validate the full requested libraries. These statements modify ONE
// source row. They do not rewrite family, library, item or viewer rows.
func usPolicyEffectDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: usMemberPolicyWrite, SQL: `UPDATE direct_memberships SET allowed_libraries=?,revision=revision+1
WHERE account_id=? AND incarnation=? AND revision=? AND disabled=0
RETURNING incarnation,revision,allowed_libraries`,
			Params:  []stmt.Param{text("policy"), text("account"), text("member_incarnation"), integer("expected_revision")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{`["movies"]`, "us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1)}}, {Name: "stale", Bindings: []any{"[]", "us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(2)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1"}, Seeks: []string{"account_id=?"}},
		},
		{ID: usProfilePolicyWrite, SQL: `UPDATE direct_profiles SET allowed_libraries=?,revision=revision+1
WHERE account_id=? AND id=? AND lifetime=? AND revision=? AND deleted=0
RETURNING lifetime,revision,allowed_libraries`,
			Params:  []stmt.Param{{Name: "policy", Type: stmt.Text, Nullable: true}, text("account"), text("profile"), text("profile_lifetime"), integer("expected_revision")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"[]", "us-target", "us-profile", "ffffffffffffffffffffffffffffffff", int64(1)}}, {Name: "inherit", Bindings: []any{nil, "us-target", "us-profile", "ffffffffffffffffffffffffffffffff", int64(1)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=? AND id=?"}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Write
		defs[n].Owner = "store/writer US library policy"
		defs[n].CallSites = []string{"store/writer/us_policy_effects.go"}
	}
	return defs
}

// Complete native target state from ONE same snapshot. SQL NULL inherits;
// explicit JSON [] denies. The value is transient request-scoped memory that
// the caller already reserved from the octet preflight, never a cached policy.
type usPolicyTargetState struct {
	policy   sql.NullString
	revision int64
}

// Native fresh-only effect, never Applied and never a replay. W still owes the
// immutable receipt, ONE scoped event on change, exact owner accounting and a
// proved FULL COMMIT. A replay must return the saved receipt and MUST NOT enter
// this function: it never reconstructs an original result from current source.
type usPolicyEffect struct {
	lifetime string
	before   usPolicyTargetState
	after    usPolicyTargetState
	changed  bool
}

// beforePolicy/revision are fresh same-turn target facts, never a client claim.
// Prepared JSON is supplied from preparation outside writer custody. Inherit
// is SQL NULL; an explicit empty profile list must remain TEXT "[]". This is
// one PK/lifetime/revision CAS update of ONE source row, or no write at all when
// the prepared policy already matches the fresh value.
func applyUSLibraryPolicy(ctx context.Context, turn *commandTurn, command *usLibraryPolicyCommand, beforePolicy sql.NullString, beforeRevision int64) (out usPolicyEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || command == nil {
		return out, ErrInvalid
	}
	if turn.transaction.descriptor.ID != command.kind ||
		command.request.expectedRevision != beforeRevision || beforeRevision < 1 || beforeRevision >= usMaxRevision {
		return out, ErrConflict
	}
	preparedPolicy := command.policy
	id, lifetime := usMemberPolicyWrite, command.request.memberIncarnation
	args := []any{preparedPolicy.String, command.request.account, command.request.memberIncarnation, beforeRevision}
	if command.kind == USProfileLibraries {
		id, lifetime = usProfilePolicyWrite, command.request.profileLifetime
		var policy any
		if preparedPolicy.Valid {
			policy = preparedPolicy.String
		}
		args = []any{policy, command.request.account, command.request.profile, command.request.profileLifetime, beforeRevision}
	} else if command.kind != USMemberLibraries || !preparedPolicy.Valid || !beforePolicy.Valid {
		// A member policy is always the explicit JSON list: nil denies, it never
		// inherits. The NOT NULL member column cannot legitimately read as NULL.
		return out, ErrInvalid
	}
	out.lifetime = lifetime
	out.before = usPolicyTargetState{policy: beforePolicy, revision: beforeRevision}
	if err = turn.checkLifetimes(ctx); err != nil {
		return usPolicyEffect{}, err
	}
	if beforePolicy == preparedPolicy {
		out.after = out.before
		return out, nil
	}
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return usPolicyEffect{}, err
	}
	var actual sql.NullString
	if err = row.Scan(&out.lifetime, &out.after.revision, &actual); errors.Is(err, sql.ErrNoRows) {
		return usPolicyEffect{}, ErrConflict
	} else if err != nil {
		return usPolicyEffect{}, err
	}
	if out.lifetime != lifetime || out.after.revision != beforeRevision+1 || actual != preparedPolicy {
		return usPolicyEffect{}, ErrResolveRequired
	}
	out.after.policy, out.changed = preparedPolicy, true
	return out, turn.checkLifetimes(ctx)
}
