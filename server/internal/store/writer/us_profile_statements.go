package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usProfilePINPresent    stmt.ID = "us.profile.pin.present"
	usProfileMemberFacts   stmt.ID = "us.profile.member.facts"
	usProfileMetadataFacts stmt.ID = "us.profile.metadata.facts"
	usProfileCreateWrite   stmt.ID = "us.profile.create.write"
	usProfileMetadataWrite stmt.ID = "us.profile.metadata.write"
)

const usProfileColumns = `account_id,id,lifetime,name,is_primary,position,art,revision,pin_revision,allowed_libraries,deleted,avatar_digest,avatar_source_mime,avatar_version,avatar_updated_at`

func usProfileEffectDefinitions() []stmt.Definition {
	txt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	profilePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=? AND id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}}}
	target := []stmt.Param{txt("account"), txt("profile"), txt("lifetime"), integer("revision")}
	bindings := []any{"us-profile-account", "us-profile", "ffffffffffffffffffffffffffffffff", int64(1)}
	defs := []stmt.Definition{
		{ID: usProfileMemberFacts, Kind: stmt.Read, SQL: `SELECT m.account_id,m.incarnation,m.revision,a.epoch FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=m.account_id WHERE m.account_id=? AND m.incarnation=? AND m.revision=? AND a.epoch=? AND m.disabled=0`, Params: []stmt.Param{txt("account"), txt("incarnation"), integer("revision"), integer("epoch")}, Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"us-profile-account", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1), int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_accounts_1"}, Seeks: []string{"account_id=?", "id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}, {Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},
		{ID: usProfileMetadataFacts, Kind: stmt.Read, SQL: `SELECT ` + usProfileColumns + ` FROM direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2 WHERE account_id=? AND id=? AND lifetime=? AND revision=? AND deleted=0`, Params: target, Cohorts: []stmt.Cohort{{Name: "current", Bindings: bindings}}, Plan: profilePlan},
		{ID: usProfileCreateWrite, Kind: stmt.Write, SQL: `INSERT INTO direct_profiles(account_id,id,lifetime,name,position,art) VALUES(?,?,?,?,?,?) RETURNING ` + usProfileColumns, Params: []stmt.Param{txt("account"), txt("profile"), txt("lifetime"), txt("name"), integer("position"), txt("art")}, Cohorts: []stmt.Cohort{{Name: "new", Bindings: []any{"us-profile-account", "us-new-profile", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "New", int64(9), "blue"}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usProfileMetadataWrite, Kind: stmt.Write, SQL: `UPDATE direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2 SET name=?,art=?,revision=revision+1 WHERE account_id=? AND id=? AND lifetime=? AND revision=? AND deleted=0 AND revision>=1 AND revision<9007199254740991 RETURNING ` + usProfileColumns, Params: append([]stmt.Param{txt("name"), txt("art")}, target...), Cohorts: []stmt.Cohort{{Name: "current", Bindings: append([]any{"Edited", "mint"}, bindings...)}}, Plan: profilePlan},
	}
	defs = append(defs, stmt.Definition{ID: usProfilePINPresent, Kind: stmt.Read, SQL: `SELECT 1 FROM direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2 WHERE account_id=? AND id=? AND lifetime=? AND revision=? AND deleted=0 AND pin_hash IS NOT NULL`, Params: target, Cohorts: []stmt.Cohort{{Name: "presence", Bindings: bindings}}, Plan: profilePlan})
	for i := range defs {
		defs[i].Owner = "store/writer US profile draft"
		defs[i].CallSites = []string{"store/writer/us_profile_statements.go"}
	}
	return defs
}

// Complete private nonsecret profile image, preserving SQL NULL versus empty.
// Root's US-001 extension must save every field plus membership incarnation,
// request identity/digest and applied time; it must NOT reconstruct on replay.
type usProfileEffect struct {
	account, profile, lifetime, name string
	primary                          bool
	position                         int64
	art                              string
	revision, pinRevision            int64
	libraries                        sql.NullString
	deleted                          bool
	avatarDigest, avatarMIME         sql.NullString
	avatarVersion                    int64
	avatarUpdatedAt                  sql.NullString
	pinRequired                      bool
}

func (v *usProfileEffect) destinations() []any {
	return []any{&v.account, &v.profile, &v.lifetime, &v.name, &v.primary, &v.position, &v.art, &v.revision, &v.pinRevision, &v.libraries, &v.deleted, &v.avatarDigest, &v.avatarMIME, &v.avatarVersion, &v.avatarUpdatedAt}
}

// SAME-TURN prerequisites owned by root: fresh profileManager/owner authority
// (including account scope/protected main profile and remembered manage window),
// original receipt resolution BEFORE fresh CAS, and FULL receipt/event/commit.
// Main-profile flags never substitute for permission. For create, root admits
// count < current membership.max_profiles (actual product maximum 8), and gives
// the current max(position)+1; position is NOT capped at 8 after retirements.
// Reserve actual point octet_length bytes for all returned variable columns
// before reading them, in this transaction; no column-times-maximum estimate.
// PIN presence uses a separate numeric point; PIN hash bytes are never read.
// The position <= usMaxRevision check is a private exact-integer recipe bound,
// not a profile-count product quota; root must refuse overflow of max+1.
// No family/user fanout, PIN, policy, selection, retirement or avatar-file effects.
// A returned source image confers no Applied outcome or acceptance authority.
func applyUSProfile(ctx context.Context, turn *commandTurn, p *usProfilePrepared, position int64) (out usProfileEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || p == nil {
		return out, ErrInvalid
	}
	d, ok := describeUSProfileCommand(p.kind)
	if !ok || turn.transaction.descriptor != d {
		return out, ErrInvalid
	}
	// Revalidate the frozen private envelope before any native effect.
	var name, art *string
	if p.setName {
		name = &p.name
	}
	if p.setArt {
		art = &p.art
	}
	checked, e := prepareUSProfileMetadata(p.target, name, art)
	if e != nil || p.kind == usProfileCreate && (!p.setName || !p.setArt || p.target.revision != 1 || position < 0 || position > usMaxRevision) {
		return out, ErrInvalid
	}
	if checked.name != p.name || checked.art != p.art {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		step, ok := turn.steps[id]
		if !ok {
			return commandRow{}, invalid("us_profile_step")
		}
		return turn.queryRow(ctx, step.statementStep, args...)
	}
	m := p.target.member
	row, e := query(usProfileMemberFacts, m.account, m.incarnation, m.expectedRevision, m.expectedEpoch)
	if e != nil {
		return out, e
	}
	var account, incarnation string
	var revision, epoch int64
	if err = row.Scan(&account, &incarnation, &revision, &epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrConflict
		}
		return out, err
	}
	if account != m.account || incarnation != m.incarnation || revision != m.expectedRevision || epoch != m.expectedEpoch {
		return out, ErrResolveRequired
	}
	want := usProfileEffect{account: m.account, profile: p.target.profile, lifetime: p.target.lifetime, name: p.name, art: p.art, position: position, revision: 1, pinRevision: 1}
	id := usProfileCreateWrite
	args := []any{m.account, p.target.profile, p.target.lifetime, p.name, position, p.art}
	if p.kind == usProfileMetadataEdit {
		row, e = query(usProfileMetadataFacts, m.account, p.target.profile, p.target.lifetime, p.target.revision)
		if e != nil {
			return out, e
		}
		if err = row.Scan(want.destinations()...); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = ErrConflict
			}
			return out, err
		}
		row, e = query(usProfilePINPresent, m.account, p.target.profile, p.target.lifetime, p.target.revision)
		if e != nil {
			return out, e
		}
		var present int64
		e = row.Scan(&present)
		if e == nil {
			if present != 1 {
				return out, ErrResolveRequired
			}
			want.pinRequired = true
		} else if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if want.account != m.account || want.profile != p.target.profile || want.lifetime != p.target.lifetime || want.revision != p.target.revision || want.deleted {
			return out, ErrResolveRequired
		}
		if p.setName {
			want.name = p.name
		}
		if p.setArt {
			want.art = p.art
		}
		want.revision++
		id = usProfileMetadataWrite
		args = []any{want.name, want.art, m.account, p.target.profile, p.target.lifetime, p.target.revision}
	}
	row, e = query(id, args...)
	if e != nil {
		return out, e
	}
	if err = row.Scan(out.destinations()...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrConflict
		}
		return out, err
	}
	out.pinRequired = want.pinRequired // unchanged; no PIN hash leaves native storage
	if out != want {
		return out, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
