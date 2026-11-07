package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usPINFencePoint   stmt.ID = "us.pin.fence.point"
	usPINFenceInsert  stmt.ID = "us.pin.fence.insert"
	usPINFenceAdvance stmt.ID = "us.pin.fence.advance"
	usPINKeepWitness  stmt.ID = "us.pin.keep.witness"
	usPINKeepFamily   stmt.ID = "us.pin.keep.family"
)

func usPINFenceDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	scope := []stmt.Param{t("kind"), t("account"), t("member"), t("profile"), t("lifetime"), t("device"), t("lineage")}
	scopeBindings := []any{"profile_pin", "target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "profile", "ffffffffffffffffffffffffffffffff", "", ""}
	columns := []string{"scope_kind", "account_id", "member_incarnation", "profile_id", "profile_lifetime", "device_id", "lineage_id"}
	binding := stmt.AutoIndexBinding{Table: "identity_security_fences", Name: "sqlite_autoindex_identity_security_fences_1", Origin: "u"}
	for _, column := range columns {
		binding.Columns = append(binding.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	defs := []stmt.Definition{
		{ID: usPINFencePoint, SQL: `SELECT id,generation,revision FROM identity_security_fences INDEXED BY sqlite_autoindex_identity_security_fences_1
WHERE scope_kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND device_id=? AND lineage_id=?`, Params: scope, Cohorts: []stmt.Cohort{{Name: "exact_scope", Bindings: scopeBindings}}, Plan: stmt.Plan{Hot: true, Indexes: []string{binding.Name}, AutoIndexes: []stmt.AutoIndexBinding{binding}, Seeks: []string{"scope_kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND device_id=? AND lineage_id=?"}}},
		{ID: usPINFenceInsert, Kind: stmt.Write, SQL: `INSERT INTO identity_security_fences(scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,device_id,lineage_id,generation,denied,revision,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id,generation,revision`, Params: append(append([]stmt.Param(nil), scope...), i("generation"), i("denied"), i("revision"), i("now")), Cohorts: []stmt.Cohort{{Name: "birth", Bindings: append(append([]any(nil), scopeBindings...), int64(1), int64(0), int64(1), int64(1000))}}, Plan: stmt.Plan{Hot: true}},
		{ID: usPINFenceAdvance, Kind: stmt.Write, SQL: `UPDATE identity_security_fences SET generation=generation+1,revision=revision+1,updated_ms=? WHERE id=? AND generation=? AND revision=? AND scope_kind='profile_pin' AND denied=0 AND generation<9007199254740991 AND revision<9007199254740991 RETURNING id,generation,revision`, Params: []stmt.Param{i("now"), i("fence"), i("generation"), i("revision")}, Cohorts: []stmt.Cohort{{Name: "next", Bindings: []any{int64(1000), int64(1), int64(1), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_security_fences"}, Seeks: []string{"rowid=?"}}},
		{ID: usPINKeepWitness, Kind: stmt.Write, SQL: `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES(?,?,?,?) ON CONFLICT(family_id,scope_kind) DO UPDATE SET fence_id=?,confirmed_generation=? RETURNING fence_id,confirmed_generation`, Params: []stmt.Param{t("family"), t("kind"), i("fence"), i("generation"), i("same_fence"), i("same_generation")}, Cohorts: []stmt.Cohort{{Name: "one_keep", Bindings: []any{"family", "profile_pin", int64(1), int64(2), int64(1), int64(2)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"authorization_family_security_fences"}}},
		{ID: usPINKeepFamily, Kind: stmt.Write, SQL: `UPDATE authorization_session_families INDEXED BY sqlite_autoindex_authorization_session_families_1 SET pin_gate=0 WHERE id=? AND account_id=? AND profile_id=? AND command_member_incarnation=? AND command_profile_lifetime=? AND current_generation=? AND revoked=0 RETURNING current_generation`, Params: []stmt.Param{t("family"), t("account"), t("profile"), t("member"), t("lifetime"), i("generation")}, Cohorts: []stmt.Cohort{{Name: "acting_only", Bindings: []any{"family", "target", "profile", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "ffffffffffffffffffffffffffffffff", int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "authorization_session_families", Name: "sqlite_autoindex_authorization_session_families_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US scalar PIN fence"
		defs[n].CallSites = []string{"store/writer/us_pin_fences.go"}
	}
	return defs
}

type usPINFenceScope struct{ account, member, profile, lifetime string }
type usPINFenceResult struct{ id, generation, revision int64 }

// Only the root can form this from its SAME-TURN independent current authority;
// being named as a family is not PIN proof. It must match the target lifetime.
type usPINKeepProof struct {
	frame  *transaction
	issuer usPolicyIssuer
}

// Internal bounded effects only. Root has already authenticated, classified the
// original identity as fresh and CASed the actual target PIN in this SAME frame.
// One scalar generation gates every other family; at most the proof-checked
// acting family gets one confirmed witness. No family enumeration or Applied.
func advanceUSProfilePINFence(ctx context.Context, turn *commandTurn, scope usPINFenceScope, now int64, keep *usPINKeepProof) (out usPINFenceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || !usIdentifier(scope.account) || !usIdentifier(scope.profile) || !usHex(scope.member, 32) || !usHex(scope.lifetime, 32) || now < 0 {
		return out, ErrInvalid
	}
	tx := turn.transaction
	if (tx.descriptor.ID != usPINSet && tx.descriptor.ID != usPINClear) || tx.descriptor.Durability != Full {
		return out, ErrInvalid
	}
	if keep != nil && (keep.frame != tx || keep.issuer.account != scope.account || keep.issuer.profile != scope.profile || keep.issuer.memberIncarnation != scope.member || keep.issuer.profileLifetime != scope.lifetime || !usIdentifier(keep.issuer.family) || keep.issuer.familyGeneration <= 0) {
		return out, ErrDisclosureDenied
	}
	row, e := usPolicyRow(ctx, turn, usPINFencePoint, "profile_pin", scope.account, scope.member, scope.profile, scope.lifetime, "", "")
	if e != nil {
		return out, e
	}
	e = row.Scan(&out.id, &out.generation, &out.revision)
	if errors.Is(e, sql.ErrNoRows) {
		row, e = usPolicyRow(ctx, turn, usPINFenceInsert, "profile_pin", scope.account, scope.member, scope.profile, scope.lifetime, "", "", int64(1), int64(0), int64(1), now)
		if e != nil {
			return out, e
		}
		e = row.Scan(&out.id, &out.generation, &out.revision)
		if e != nil {
			return out, e
		}
		if out.id <= 0 || out.generation != 1 || out.revision != 1 {
			return out, ErrResolveRequired
		}
	} else if e != nil {
		return out, e
	} else {
		before := out
		if before.id <= 0 || before.generation < 1 || before.generation >= usMaxRevision || before.revision < 1 || before.revision >= usMaxRevision {
			return out, ErrResolveRequired
		}
		row, e = usPolicyRow(ctx, turn, usPINFenceAdvance, now, before.id, before.generation, before.revision)
		if e != nil {
			return out, e
		}
		if e = row.Scan(&out.id, &out.generation, &out.revision); e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if out.id != before.id || out.generation != before.generation+1 || out.revision != before.revision+1 {
			return out, ErrResolveRequired
		}
	}
	if keep != nil {
		family := keep.issuer
		row, e = usPolicyRow(ctx, turn, usPINKeepFamily, family.family, family.account, family.profile, family.memberIncarnation, family.profileLifetime, family.familyGeneration)
		if e != nil {
			return out, e
		}
		var generation int64
		if e = row.Scan(&generation); e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if generation != family.familyGeneration {
			return out, ErrResolveRequired
		}
		row, e = usPolicyRow(ctx, turn, usPINKeepWitness, family.family, "profile_pin", out.id, out.generation, out.id, out.generation)
		if e != nil {
			return out, e
		}
		var id, confirmed int64
		if e = row.Scan(&id, &confirmed); e != nil {
			return out, e
		}
		if id != out.id || confirmed != out.generation {
			return out, ErrResolveRequired
		}
	}
	return out, turn.checkLifetimes(ctx)
}
