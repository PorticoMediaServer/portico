package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usMemberTargetFacts     stmt.ID = "us.member.target.facts"
	usMemberRoleAdminWrite  stmt.ID = "us.member.role.admin.write"
	usMemberRoleMemberWrite stmt.ID = "us.member.role.member.write"
	usMemberDisableWrite    stmt.ID = "us.member.disable.write"
	usAccountEpochWrite     stmt.ID = "us.account.epoch.write"
)

// Source comparisons run immediately before the one-row write in the SAME
// private writer transaction. HN's closed DML grammar excludes subqueries and
// UPDATE FROM, so the point join validates the other source row independently.
// No concurrent writer can change that source between these finite steps.
func usMemberEffectDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	memberIndex := stmt.AutoIndexBinding{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}
	accountIndex := stmt.AutoIndexBinding{Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	memberPlan := stmt.Plan{Hot: true, Indexes: []string{memberIndex.Name}, Seeks: []string{"account_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{memberIndex}}
	accountPlan := stmt.Plan{Hot: true, Indexes: []string{accountIndex.Name}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{accountIndex}}
	factsPlan := stmt.Plan{Hot: true, Indexes: []string{memberIndex.Name, accountIndex.Name}, Seeks: []string{"account_id=?", "id=?"}, AutoIndexes: []stmt.AutoIndexBinding{memberIndex, accountIndex}}
	params := []stmt.Param{text("account"), text("member_incarnation"), integer("expected_revision")}
	const target = ` WHERE account_id=? AND incarnation=? AND revision=? AND role IN('admin','member') AND revision>=1 AND revision<9007199254740991`
	const result = ` RETURNING account_id,incarnation,role,revision,disabled,disabled_ms,disabled_reason`
	cohort := []stmt.Cohort{{Name: "current", Bindings: []any{"us-member", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1)}}}
	defs := []stmt.Definition{
		{ID: usMemberTargetFacts, Kind: stmt.Read, SQL: `SELECT m.account_id,m.incarnation,m.role,m.revision,m.disabled,m.disabled_ms,m.disabled_reason,a.epoch
FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=m.account_id
WHERE m.account_id=? AND m.incarnation=? AND m.revision=? AND a.epoch=?`,
			Params:  append(append([]stmt.Param(nil), params...), integer("expected_epoch")),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"us-member", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1), int64(1)}}}, Plan: factsPlan},
		{ID: usMemberRoleAdminWrite, Kind: stmt.Write, SQL: `UPDATE direct_memberships INDEXED BY sqlite_autoindex_direct_memberships_1 SET role='admin',revision=revision+1` + target + result,
			Params: params, Cohorts: cohort, Plan: memberPlan},
		{ID: usMemberRoleMemberWrite, Kind: stmt.Write, SQL: `UPDATE direct_memberships INDEXED BY sqlite_autoindex_direct_memberships_1 SET role='member',revision=revision+1` + target + result,
			Params: params, Cohorts: cohort, Plan: memberPlan},
		{ID: usMemberDisableWrite, Kind: stmt.Write, SQL: `UPDATE direct_memberships INDEXED BY sqlite_autoindex_direct_memberships_1 SET disabled=1,disabled_ms=?,disabled_reason=?,revision=revision+1` + target + result,
			Params:  append([]stmt.Param{integer("disabled_at"), text("disabled_reason")}, params...),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{int64(1000), "owner", "us-member", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1)}}}, Plan: memberPlan},
		{ID: usAccountEpochWrite, Kind: stmt.Write, SQL: `UPDATE accounts INDEXED BY sqlite_autoindex_accounts_1 SET epoch=epoch+1 WHERE id=? AND epoch=? AND epoch>=1 AND epoch<9007199254740991 RETURNING id,epoch`,
			Params:  []stmt.Param{text("account"), integer("expected_epoch")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"us-member", int64(1)}}}, Plan: accountPlan},
	}
	for n := range defs {
		defs[n].Owner = "store/writer US membership draft"
		defs[n].CallSites = []string{"store/writer/us_member_statements.go"}
	}
	return defs
}

// This is a source comparison, never issuer authority. The bounded point join
// returns only fixed-domain strings/scalars and exact bounded identifier keys.
func readUSMemberTarget(ctx context.Context, turn *commandTurn, target usMemberTarget) (out usMemberEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !validUSMemberTarget(target) {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	step, ok := turn.steps[usMemberTargetFacts]
	if !ok {
		return out, invalid("us_member_facts_step")
	}
	row, err := turn.queryRow(ctx, step.statementStep, target.account, target.incarnation, target.expectedRevision, target.expectedEpoch)
	if err != nil {
		return out, err
	}
	var epoch int64
	if err = row.Scan(&out.account, &out.incarnation, &out.role, &out.revision, &out.disabled, &out.disabledMS, &out.disabledReason, &epoch); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if out.account != target.account || out.incarnation != target.incarnation || out.revision != target.expectedRevision || epoch != target.expectedEpoch {
		return out, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}

// Actual native post-scalars for root's explicit US-001 receipt extension. No
// generic body, permission object or Applied outcome. Role changes (even equal
// values) bump revision as access.SetRole does; disabling again remains an edit.
type usMemberEffect struct {
	account, incarnation, role string
	revision                   int64
	disabled                   bool
	disabledMS                 int64
	disabledReason             string
}

type usAccountEpochEffect struct {
	account string
	epoch   int64
}

// Required SAME-TURN preconditions, owned by root:
//   - authenticate the current issuer, source lifetimes and current owner/admin
//     authority BEFORE disclosing/classifying the original immutable receipt;
//   - role changes require access.SetRole's no-self rule and ManagesTier on BOTH
//     current and desired target tiers, using live authority, never a role claim.
//     Owner targets and promotion to owner are excluded here natively. Neither
//     role nor disable can remove the last owner: ownership transfer is separate;
//   - disable follows identity.UpdateDirectMember: an admin manages members only;
//     owner manages admins/members. Self-disable is not granted by this helper;
//   - for disable, advance the target epoch atomically with this edit (using the
//     POST membership revision for its CAS), then install root's native fences,
//     scoped event and immutable result. Epoch/incarnation/disabled/live tier are
//     checked on every subsequent request; never retier/revoke all families;
//   - preserve unrelated allowed libraries. The API's combined limits edit, if
//     needed, is separately composed by root rather than implied by disable;
//   - root alone composes receipt accounting and proves FULL COMMIT. An effect
//     alone is not success, current authority, a receipt or admission.
//
// nowMS is trusted same-turn time from root, never a caller-selected timestamp.
func applyUSMember(ctx context.Context, turn *commandTurn, prepared *usMemberPrepared, nowMS int64) (out usMemberEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || prepared == nil {
		return out, ErrInvalid
	}
	d, ok := describeUSMemberCommand(prepared.kind)
	if !ok || turn.transaction.descriptor != d || prepared.kind == usAccountEpochAdvance {
		return out, ErrInvalid
	}
	in := prepared.request
	if !validUSMemberTarget(in.target) || in.target.expectedRevision >= usMaxRevision {
		return out, ErrInvalid
	}
	if prepared.kind == usMemberRoleChange && in.role != "admin" && in.role != "member" || prepared.kind == usMembershipDisable && in.role != "" {
		return out, ErrInvalid
	}
	if prepared.kind == usMembershipDisable && (nowMS <= 0 || nowMS > usMaxRevision) {
		return out, invalid("us_member_time")
	}
	before, err := readUSMemberTarget(ctx, turn, in.target)
	if err != nil {
		return out, err
	}
	id := usMemberRoleMemberWrite
	if in.role == "admin" {
		id = usMemberRoleAdminWrite
	}
	args := []any{in.target.account, in.target.incarnation, in.target.expectedRevision}
	wantDisabledMS, wantDisabledReason := before.disabledMS, before.disabledReason
	if prepared.kind == usMembershipDisable {
		id = usMemberDisableWrite
		if wantDisabledMS <= 0 {
			wantDisabledMS = nowMS
		}
		if wantDisabledReason == "" {
			wantDisabledReason = "owner"
		}
		args = append([]any{wantDisabledMS, wantDisabledReason}, args...)
	}
	step, ok := turn.steps[id]
	if !ok {
		return out, invalid("us_member_write_step")
	}
	row, err := turn.queryRow(ctx, step.statementStep, args...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.account, &out.incarnation, &out.role, &out.revision, &out.disabled, &out.disabledMS, &out.disabledReason); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if out.account != in.target.account || out.incarnation != in.target.incarnation || out.revision != in.target.expectedRevision+1 ||
		(out.role != "admin" && out.role != "member") ||
		prepared.kind == usMemberRoleChange && out.role != in.role ||
		prepared.kind == usMembershipDisable && (!out.disabled || out.disabledMS <= 0 || out.disabledReason == "") {
		return out, ErrResolveRequired
	}
	if out.disabledMS != wantDisabledMS || out.disabledReason != wantDisabledReason ||
		prepared.kind == usMemberRoleChange && out.disabled != before.disabled ||
		prepared.kind == usMembershipDisable && out.role != before.role {
		return out, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}

// Root chooses only an existing API purpose: e.g. authorized sign-out everywhere
// or the epoch leg of membership disable. This primitive grants neither an
// administrator's power over owners nor self/admin password-reset authority.
// The returned epoch is native; the membership lifetime/revision are CAS-only.
func applyUSAccountEpoch(ctx context.Context, turn *commandTurn, prepared *usAccountEpochPrepared) (out usAccountEpochEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || prepared == nil {
		return out, ErrInvalid
	}
	d, _ := describeUSMemberCommand(usAccountEpochAdvance)
	// Disable may compose this bounded second leg under its OWN FULL purpose.
	disable, _ := describeUSMemberCommand(usMembershipDisable)
	if turn.transaction.descriptor != d && turn.transaction.descriptor != disable {
		return out, ErrInvalid
	}
	in := prepared.target
	if _, err = readUSMemberTarget(ctx, turn, in); err != nil {
		return out, err
	}
	step, ok := turn.steps[usAccountEpochWrite]
	if !ok {
		return out, invalid("us_account_epoch_step")
	}
	row, err := turn.queryRow(ctx, step.statementStep, in.account, in.expectedEpoch)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.account, &out.epoch); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if out.account != in.account || out.epoch != in.expectedEpoch+1 {
		return out, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
