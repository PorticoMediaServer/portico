package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usPINSetWrite   stmt.ID = "us.pin.profile.set"
	usPINClearWrite stmt.ID = "us.pin.profile.clear"
)

// Static one-profile native effects, with no family/grant/attempt fan-out. Root
// must compose the immediate PIN/remember/lineage fences from US-001 and at most
// one proof-checked keep-family. A profile write alone does not implement reentry.
func usPINEffectDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	params := []stmt.Param{text("account"), text("profile"), text("profile_lifetime"), integer("expected_revision"), integer("expected_pin_revision")}
	const target = ` WHERE account_id=? AND id=? AND lifetime=? AND revision=? AND pin_revision=? AND deleted=0
AND revision>=1 AND revision<9007199254740991 AND pin_revision>=1 AND pin_revision<9007199254740991
RETURNING account_id,id,lifetime,revision,pin_revision,pin_hash`
	const prefix = `UPDATE direct_profiles SET `
	defs := []stmt.Definition{
		{ID: usPINSetWrite, SQL: prefix + `pin_hash=?,revision=revision+1,pin_revision=pin_revision+1` + target,
			Params:  append([]stmt.Param{{Name: "prepared_hash", Type: stmt.Blob}}, params...),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{[]byte(usPINFixtureHash), "us-pin-account", "us-pin-profile", "ffffffffffffffffffffffffffffffff", int64(1), int64(1)}}}},
		{ID: usPINClearWrite, SQL: prefix + `pin_hash=?,revision=revision+1,pin_revision=pin_revision+1` + target,
			Params:  append([]stmt.Param{{Name: "cleared_hash", Type: stmt.Blob, Nullable: true}}, params...),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{nil, "us-pin-account", "us-pin-profile", "ffffffffffffffffffffffffffffffff", int64(1), int64(1)}}}},
	}
	for n := range defs {
		defs[n].Kind = stmt.Write
		defs[n].Owner = "store/writer US PIN profile draft"
		defs[n].CallSites = []string{"store/writer/us_pin_statements.go"}
		defs[n].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=? AND id=?"}}
	}
	return defs
}

// A synthetic encoding-only cohort, never a credential or issued identity.
const usPINFixtureHash = "$2a$08$....................................................."

// Internal scalar native post-result, never Applied. No hash/PIN may escape in
// a public result, receipt/event payload or error. Same-value edits still bump
// both revisions, including clearing an already absent PIN, as the API does.
type usPINEffect struct {
	account, profile, lifetime string
	revision, pinRevision      int64
	pinRequired                bool
}

// Required SAME-TURN preconditions, owned by root's composition:
//   - authenticate current issuer and all lifetimes before receipt disclosure;
//     resolve original identity/digest before evaluating fresh target CAS;
//   - self edit requires profileManager semantics: an account-scoped family
//     cannot edit through a protected main profile; remembered main-profile
//     management requires its fresh PIN window. Account-management PIN recovery
//     retains its separate account-scoped route and the same manage-window gate;
//   - managed reset can only CLEAR another person's PIN, after proving current
//     tiers (owner -> admin/member, admin -> member). Main-profile status alone
//     grants nothing; a selected child/viewer or narrow grant cannot manage;
//   - after this effect, atomically install US-001 credential/remember/lineage
//     fences and any proved keep-family, actual immutable receipt and scoped
//     event. Clearing preserves sign-ins: with no PIN there is no reentry gate.
//
// This function owns no admission, authority, receipt, COMMIT or accepting facade.
func applyUSPIN(ctx context.Context, turn *commandTurn, prepared *usPINPrepared) (out usPINEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || prepared == nil {
		return out, ErrInvalid
	}
	descriptor, ok := describeUSPINCommand(prepared.kind)
	if !ok || turn.transaction.descriptor != descriptor {
		return out, ErrInvalid
	}
	in := prepared.request
	id := usPINClearWrite
	// Only this closed effect supplies the clear binding: SQL NULL, never an
	// empty BLOB. Named RETURNING columns keep HN's native DML grammar intact.
	args := []any{nil, in.account, in.profile, in.profileLifetime, in.expectedRevision, in.expectedPINRevision}
	if prepared.kind == usPINSet {
		id = usPINSetWrite
		args[0] = in.hash
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	step, ok := turn.steps[id]
	if !ok {
		return out, invalid("us_pin_write_step")
	}
	row, err := turn.queryRow(ctx, step.statementStep, args...)
	if err != nil {
		return out, err
	}
	// The post-write hash is bounded to the prepared 60 bytes or SQL NULL. It
	// stays inside this effect, where presence/exact bytes are checked; only
	// the scalar flag leaves. Never copy it into a result, error or receipt.
	var actualHash []byte
	if err = row.Scan(&out.account, &out.profile, &out.lifetime, &out.revision, &out.pinRevision, &actualHash); errors.Is(err, sql.ErrNoRows) {
		return usPINEffect{}, ErrConflict
	} else if err != nil {
		return usPINEffect{}, err
	}
	out.pinRequired = actualHash != nil
	if out.account != in.account || out.profile != in.profile || out.lifetime != in.profileLifetime ||
		out.revision != in.expectedRevision+1 || out.pinRevision != in.expectedPINRevision+1 ||
		out.pinRequired != (prepared.kind == usPINSet) || !bytes.Equal(actualHash, in.hash) {
		return usPINEffect{}, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
