package writer

import (
	"bytes"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	basicCopyHeader    stmt.ID = "scan.basic_copy.header"
	basicCopyCursorCAS stmt.ID = "scan.basic_copy.cursor_cas"
)

// The complete copy-phase header. Every copy turn reads it once with its exact
// entry cursor and ends with ONE cursor CAS that rewrites every cursor column.
const basicCopyHeaderPredicate = ` WHERE id=? AND asset_id=? AND asset_token=? AND slot=0 AND phase=2 AND expected_epoch=? AND input_revision=? AND family_mask=511 AND fingerprint=? AND job_revision=? AND has_after=1 AND after_entity=? AND upper_entity=? AND copy_family=? AND copy_phase=? AND copy_id=? AND copy_aux=? AND copy_key=? AND copy_key2=? AND copy_key3=? AND target_available=1 AND target_shape_id=? AND target_owner_object IS NULL AND target_owner_revision IS NULL AND fault='' AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0`

func basicCopyHeaderParams() []stmt.Param {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	return []stmt.Param{i("change_id"), i("asset_id"), t("asset_token"), i("expected_epoch"), i("input_revision"), b("fingerprint"), i("job_revision"),
		i("after_entity"), i("upper_entity"), i("copy_family"), i("copy_phase"), i("copy_id"), i("copy_aux"), t("copy_key"), t("copy_key2"), t("copy_key3"), i("shape_id")}
}

func basicCopyHeaderCohort() []any {
	return []any{int64(51), int64(41), "basic-asset", int64(17), int64(1), bytes.Repeat([]byte{0x31}, 32), int64(4),
		int64(31), int64(31), int64(0), int64(0), int64(0), int64(0), "", "", "", int64(61)}
}

func basicCopyDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopyHeader, Kind: stmt.Read, SQL: `SELECT id FROM catalog_info_changes` + basicCopyHeaderPredicate,
			Params: basicCopyHeaderParams(), Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "copy_entry_cursor", Bindings: basicCopyHeaderCohort()}}},
		{ID: basicCopyCursorCAS, Kind: stmt.Write,
			SQL:     `UPDATE catalog_info_changes SET phase=?,copy_family=?,copy_phase=?,copy_id=?,copy_aux=?,copy_key=?,copy_key2=?,copy_key3=?,job_revision=?` + basicCopyHeaderPredicate + ` RETURNING phase,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3,job_revision`,
			Params:  append([]stmt.Param{i("next_phase"), i("next_copy_family"), i("next_copy_phase"), i("next_copy_id"), i("next_copy_aux"), t("next_copy_key"), t("next_copy_key2"), t("next_copy_key3"), i("next_job_revision")}, basicCopyHeaderParams()...),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "advance_copy_cursor", Bindings: append([]any{int64(2), int64(2), int64(0), int64(0), int64(0), "", "", "", int64(5)}, basicCopyHeaderCohort()...)}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy.go"}
	}
	return defs
}
