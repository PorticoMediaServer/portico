package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Static one-profile native PIN effects (532 set, 533 clear), with no
// family/grant/attempt fan-out. Root must compose the immediate PIN/remember/
// lineage fences from US-02 and at most one proof-checked keep-family. A profile
// write alone does not implement reentry: every other family of the profile is
// gated by the scalar fence generation, never by enumerating or rewriting them.
//
// The preparation is frozen OUTSIDE writer custody. Hashing and PIN verification
// never run here, and no hash is copied into a result, receipt, event, log or
// error: the effect keeps the post-write hash inside its own frame only to check
// presence and the exact prepared bytes, then publishes the scalar flag alone.
const (
	usPINSetWrite   stmt.ID = "us.pin.profile.set"
	usPINClearWrite stmt.ID = "us.pin.profile.clear"
)

// Synthetic encoding-only cohort value, never a credential or issued PIN.
const usPINFixtureHash = "$2a$08$....................................................."

func usPINEffectDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	params := []stmt.Param{text("account"), text("profile"), text("profile_lifetime"), integer("expected_revision"), integer("expected_pin_revision")}
	const target = ` WHERE account_id=? AND id=? AND lifetime=? AND revision=? AND pin_revision=? AND deleted=0
AND revision>=1 AND revision<9007199254740991 AND pin_revision>=1 AND pin_revision<9007199254740991
RETURNING account_id,id,lifetime,revision,pin_revision,pin_hash`
	const prefix = `UPDATE direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2 SET `
	point := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u",
			Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}}}
	defs := []stmt.Definition{
		{ID: usPINSetWrite, SQL: prefix + `pin_hash=?,revision=revision+1,pin_revision=pin_revision+1` + target,
			Params:  append([]stmt.Param{{Name: "prepared_hash", Type: stmt.Blob}}, params...),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{[]byte(usPINFixtureHash), "us-pin-account", "us-pin-profile", "ffffffffffffffffffffffffffffffff", int64(1), int64(1)}}}, Plan: point},
		{ID: usPINClearWrite, SQL: prefix + `pin_hash=?,revision=revision+1,pin_revision=pin_revision+1` + target,
			Params:  append([]stmt.Param{{Name: "cleared_hash", Type: stmt.Blob, Nullable: true}}, params...),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{nil, "us-pin-account", "us-pin-profile", "ffffffffffffffffffffffffffffffff", int64(1), int64(1)}}}, Plan: point},
	}
	for n := range defs {
		defs[n].Kind = stmt.Write
		defs[n].Owner = "store/writer US PIN profile"
		defs[n].CallSites = []string{"store/writer/us_pin_statements.go"}
	}
	return defs
}

// Internal scalar native post-result, never Applied. Same-value edits still bump
// BOTH revisions, including clearing an already absent PIN, exactly as the API
// does. The hash never leaves this effect.
type usPINEffect struct {
	account, profile, lifetime string
	revision, pinRevision      int64
	pinRequired                bool
}

// Required SAME-TURN preconditions, owned by root's composition:
//   - authenticate current issuer and all lifetimes before receipt disclosure;
//     resolve the original identity/digest before evaluating fresh target CAS;
//   - this task admits the CALLER's OWN account only. A managed reset of another
//     account requires its own reviewed tier authority recipe and is refused
//     here rather than inferred from a target membership role;
//   - self edit requires the current US-01 owner/main-profile recipe; a
//     remembered main profile whose manage window lapsed, an account-scoped or
//     narrow grant is not a manager of this route;
//   - after this effect, atomically install the US-02 scalar PIN fence and at
//     most one proved keep-family, the immutable receipt and, for a fresh
//     original, the complete US-04 MemberProfile image. Clearing preserves
//     sign-ins: with no PIN there is no reentry gate.
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
	// the scalar flag leaves.
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
