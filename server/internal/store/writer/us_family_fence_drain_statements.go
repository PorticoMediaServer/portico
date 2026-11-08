package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usFenceDrainFamily    stmt.ID = "us.family.fence_drain.family"
	usFenceDrainChild     stmt.ID = "us.family.fence_drain.child"
	usFenceDrainFence     stmt.ID = "us.family.fence_drain.fence"
	usFenceDrainDelete    stmt.ID = "us.family.fence_drain.delete"
	usFenceDrainRemaining stmt.ID = "us.family.fence_drain.remaining"
)

func usFamilyFenceDrainDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: usFenceDrainFamily, SQL: `SELECT 1 FROM authorization_session_families INDEXED BY sqlite_autoindex_authorization_session_families_1 WHERE id=? AND server_id=? AND account_id=? AND profile_id=? AND command_member_incarnation=? AND command_profile_lifetime=? AND command_incarnation=? AND current_generation=? AND command_last_sequence=? AND command_acknowledged_sequence=? AND device_id=? AND pin_lineage_id=? AND authority='local' AND revoked=1 AND security_kind IN(0,1)`, Params: []stmt.Param{t("family"), t("server"), t("account"), t("profile"), t("member"), t("lifetime"), b("incarnation"), i("generation"), i("last"), i("ack"), t("device"), t("lineage")}},
		{ID: usFenceDrainChild, SQL: `SELECT fence_id,confirmed_generation FROM authorization_family_security_fences WHERE family_id=? AND scope_kind=?`, Params: []stmt.Param{t("family"), t("scope")}},
		{ID: usFenceDrainFence, SQL: `SELECT 1 FROM identity_security_fences WHERE id=? AND scope_kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND device_id=? AND lineage_id=?`, Params: []stmt.Param{i("fence"), t("scope"), t("account"), t("member"), t("profile"), t("lifetime"), t("device"), t("lineage")}},
		{ID: usFenceDrainDelete, Kind: stmt.Write, SQL: `DELETE FROM authorization_family_security_fences WHERE family_id=? AND scope_kind=? AND fence_id=? AND confirmed_generation=? RETURNING 1`, Params: []stmt.Param{t("family"), t("scope"), i("fence"), i("generation")}},
		{ID: usFenceDrainRemaining, SQL: `SELECT 1 FROM authorization_family_security_fences WHERE family_id=? LIMIT 1`, Params: []stmt.Param{t("family")}},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US private family fence retirement"
		defs[n].CallSites = []string{"store/writer/us_family_fence_drain.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
	}
	return defs
}
