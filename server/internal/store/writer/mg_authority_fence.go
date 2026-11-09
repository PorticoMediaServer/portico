package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const mgAuthorityFenceWitness stmt.ID = "mg.authority.fence-witness"
const mgAuthorityFenceCurrent stmt.ID = "mg.authority.fence-current"

// Two complete-key points preserve the US conjunction. Unlike an analyzed
// one-row join, neither point can choose a scan over the fence population.
func mgAuthorityFenceDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: mgAuthorityFenceWitness, SQL: `SELECT fence_id,confirmed_generation FROM authorization_family_security_fences WHERE family_id=? AND scope_kind=?`, Params: []stmt.Param{text("family"), text("scope")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"authorization_family_security_fences"}, Seeks: []string{"family_id=? AND scope_kind=?"}}, Cohorts: []stmt.Cohort{{Name: "family-scope", Bindings: []any{"issuer-family", "device"}}}},
		{ID: mgAuthorityFenceCurrent, SQL: `SELECT 1 FROM identity_security_fences WHERE id=? AND scope_kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND device_id=? AND lineage_id=? AND generation=? AND denied=0`, Params: []stmt.Param{integer("fence"), text("scope"), text("account"), text("member"), text("profile"), text("lifetime"), text("device"), text("lineage"), integer("confirmed_generation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_security_fences"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "current-fence", Bindings: []any{int64(1), "device", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", "", "issuer-device", "", int64(1)}}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.mg.authority"
		defs[i].CallSites = []string{"writer/mg_authority_fence.go"}
		defs[i].Kind = stmt.Read
	}
	return defs
}
func mgAuthorityFenceCheck(ctx context.Context, t *commandTurn, w usOwnerAuthorityWitness, kind string) error {
	profile, lifetime, device, lineage := "", "", "", ""
	switch kind {
	case "device":
		device = w.device
	case "profile_pin", "profile_remember":
		profile, lifetime = w.issuer.profile, w.issuer.profileLifetime
	case "profile_lineage":
		profile, lifetime, lineage = w.issuer.profile, w.issuer.profileLifetime, w.lineage
	default:
		return ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, t, mgAuthorityFenceWitness, w.issuer.family, kind)
	if err != nil {
		return err
	}
	var fence, generation int64
	if err = row.Scan(&fence, &generation); err != nil {
		return usAuthorityReadError(err)
	}
	if fence <= 0 || generation <= 0 {
		return ErrResolveRequired
	}
	return usAuthorityPresence(ctx, t, mgAuthorityFenceCurrent, true, fence, kind, w.issuer.account, w.issuer.memberIncarnation, profile, lifetime, device, lineage, generation)
}
