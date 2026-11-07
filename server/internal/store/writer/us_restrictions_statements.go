package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usRestrictionsSource stmt.ID = "us.restrictions.source"
	usRestrictionsFacts  stmt.ID = "us.restrictions.facts"
	usRestrictionsInsert stmt.ID = "us.restrictions.insert"
	usRestrictionsUpdate stmt.ID = "us.restrictions.update"
)
const usRestrictionsColumns = `profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision`

// Fixed bounded points only. Root owns current manager/owner authorization,
// source octet preflight, complete result+labels receipt, events and FULL commit.
// HN excludes write subqueries: source lifetime checks and the single write
// must execute consecutively in the same private writer transaction.
func usRestrictionsEffectDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	index := stmt.AutoIndexBinding{Table: "profile_restrictions", Name: "sqlite_autoindex_profile_restrictions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}}}
	plan := stmt.Plan{Hot: true, Indexes: []string{index.Name}, Seeks: []string{"profile_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{index}}
	values := []stmt.Param{t("system"), t("rating"), i("age"), b("unrated"), t("labels"), b("downloads"), b("live"), b("dvr"), b("together")}
	defs := []stmt.Definition{
		{ID: usRestrictionsFacts, Kind: stmt.Read, SQL: `SELECT ` + usRestrictionsColumns + ` FROM profile_restrictions INDEXED BY sqlite_autoindex_profile_restrictions_1 WHERE profile_id=?`, Params: []stmt.Param{t("profile")}, Plan: plan},
		{ID: usRestrictionsInsert, Kind: stmt.Write, SQL: `INSERT INTO profile_restrictions(rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,profile_id,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(profile_id) DO NOTHING RETURNING ` + usRestrictionsColumns, Params: append(append([]stmt.Param{}, values...), t("profile"), i("new_revision")), Plan: stmt.Plan{Hot: true, Indexes: []string{index.Name}}},
		{ID: usRestrictionsUpdate, Kind: stmt.Write, SQL: `UPDATE profile_restrictions INDEXED BY sqlite_autoindex_profile_restrictions_1 SET rating_system=?,maximum_age_rating=?,maximum_age=?,allow_unrated=?,blocked_labels=?,allow_downloads=?,allow_live_tv=?,allow_dvr=?,allow_watch_together=?,revision=revision+1 WHERE profile_id=? AND revision=? AND allow_dvr=? RETURNING ` + usRestrictionsColumns, Params: append(append([]stmt.Param{}, values...), t("profile"), i("revision"), b("current_dvr")), Plan: plan},
	}
	// Reuse proven profile/member point plans but do not register shared hooks.
	source := usProfileEffectDefinitions()[0]
	source.ID = usRestrictionsSource
	source.SQL = `SELECT m.role,p.is_primary FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=m.account_id CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=m.account_id WHERE m.account_id=? AND m.incarnation=? AND m.revision=? AND a.epoch=? AND m.disabled=0 AND p.id=? AND p.lifetime=? AND p.revision=? AND p.deleted=0`
	source.Params = append(source.Params, t("profile"), t("lifetime"), i("profile_revision"))
	source.Cohorts = nil
	pi := stmt.AutoIndexBinding{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}
	source.Plan.Indexes = append(source.Plan.Indexes, pi.Name)
	source.Plan.Seeks = append(source.Plan.Seeks, "account_id=? AND id=?")
	source.Plan.AutoIndexes = append(source.Plan.AutoIndexes, pi)
	defs = append(defs, source)
	samples := map[stmt.ID][]any{
		usRestrictionsSource: {"us-profile-account", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1), int64(1), "us-profile", "ffffffffffffffffffffffffffffffff", int64(1)},
		usRestrictionsFacts:  {"us-profile"},
		usRestrictionsInsert: {"", "", int64(-1), true, "[]", true, true, false, true, "us-profile", int64(2)},
		usRestrictionsUpdate: {"", "", int64(-1), true, "[]", true, true, false, true, "us-profile", int64(2), false},
	}
	for n := range defs {
		defs[n].Cohorts = []stmt.Cohort{{Name: "point", Bindings: samples[defs[n].ID]}}
		defs[n].Owner = "store/writer US restrictions draft"
		defs[n].CallSites = []string{"store/writer/us_restrictions_statements.go"}
	}
	return defs
}

// Native projection only, never an Applied outcome or receipt authority.
type usRestrictionsEffect struct {
	profile, ratingSystem, maximumAgeRating                   string
	maximumAge                                                int
	allowUnrated                                              bool
	labelsJSON                                                string
	allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
	revision                                                  int64
}

func (v *usRestrictionsEffect) destinations() []any {
	return []any{&v.profile, &v.ratingSystem, &v.maximumAgeRating, &v.maximumAge, &v.allowUnrated, &v.labelsJSON, &v.allowDownloads, &v.allowLiveTV, &v.allowDVR, &v.allowWatchTogether, &v.revision}
}
func (usRestrictionsEffect) String() string     { return "usRestrictionsEffect{redacted}" }
func (v usRestrictionsEffect) GoString() string { return v.String() }
func (usRestrictionsEffect) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsEffect")
}

// Additional DVR-enablement authority is a frame-bound current owner stamp,
// never a caller boolean. Parent still owes current general manager authority.
type usRestrictionsOwnerProof struct{ authority *usPolicyAuthorization }

func (p *usRestrictionsOwnerProof) check(turn *commandTurn, issuer usPolicyIssuer) error {
	if p == nil || p.authority == nil || p.authority.issuer != issuer {
		return ErrDisclosureDenied
	}
	return p.authority.check(turn)
}
func (usRestrictionsOwnerProof) String() string   { return "usRestrictionsOwnerProof{redacted}" }
func (usRestrictionsOwnerProof) GoString() string { return "usRestrictionsOwnerProof{redacted}" }
func (usRestrictionsOwnerProof) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsOwnerProof")
}

// Descriptor equality bounds purpose; it does not establish current authority.
func applyUSRestrictions(ctx context.Context, turn *commandTurn, c *usRestrictionsCommand, ownerProof *usRestrictionsOwnerProof) (out usRestrictionsEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil {
		return out, ErrInvalid
	}
	d, e := Describe(usProfileRestrictionsEdit)
	if e != nil || turn.transaction.descriptor != d {
		return out, ErrInvalid
	}
	r := c.request
	labels := make([]string, len(r.labels))
	for n, v := range r.labels {
		labels[n] = string(v)
	}
	checked, e := prepareUSRestrictions(c.identity, r.ownerID, r.issuer, r.target, usRestrictionsEdit{r.expectedRevision, r.ratingSystem, r.maximumAgeRating, r.allowUnrated, labels, r.allowDownloads, r.allowLiveTV, r.allowDVR, r.allowWatchTogether})
	if e != nil || checked.digest != c.digest || !reflect.DeepEqual(checked.request, r) {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		step, ok := turn.steps[id]
		if !ok {
			return commandRow{}, ErrInvalid
		}
		return turn.queryRow(ctx, step.statementStep, args...)
	}
	p := r.target.profile
	m := p.member
	row, e := query(usRestrictionsSource, m.account, m.incarnation, m.expectedRevision, m.expectedEpoch, p.profile, p.lifetime, p.revision)
	if e != nil {
		return out, e
	}
	var role string
	var primary bool
	if e = row.Scan(&role, &primary); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			e = ErrConflict
		}
		return out, e
	}
	row, e = query(usRestrictionsFacts, p.profile)
	if e != nil {
		return out, e
	}
	var current usRestrictionsEffect
	e = row.Scan(current.destinations()...)
	if errors.Is(e, sql.ErrNoRows) {
		if r.target.present || r.target.currentDVR != (primary && role == "owner") {
			return out, ErrConflict
		}
	} else if e != nil {
		return out, e
	} else if !r.target.present || current.revision != r.target.revision || current.allowDVR != r.target.currentDVR {
		return out, ErrConflict
	}
	if r.allowDVR && !r.target.currentDVR {
		if err = ownerProof.check(turn, r.issuer); err != nil {
			return out, err
		}
	}
	args := []any{r.ratingSystem, r.maximumAgeRating, int64(r.maximumAge), r.allowUnrated, r.labelsJSON, r.allowDownloads, r.allowLiveTV, r.allowDVR, r.allowWatchTogether, p.profile}
	id := usRestrictionsInsert
	if r.target.present {
		id = usRestrictionsUpdate
		args = append(args, r.target.revision, r.target.currentDVR)
	} else {
		args = append(args, int64(2))
	}
	row, e = query(id, args...)
	if e != nil {
		return out, e
	}
	if e = row.Scan(out.destinations()...); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			e = ErrConflict
		}
		return out, e
	}
	want := usRestrictionsEffect{p.profile, r.ratingSystem, r.maximumAgeRating, r.maximumAge, r.allowUnrated, r.labelsJSON, r.allowDownloads, r.allowLiveTV, r.allowDVR, r.allowWatchTogether, r.expectedRevision + 1}
	if out != want {
		return out, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
