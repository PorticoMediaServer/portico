package writer

import (
	"bytes"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const basicAcceptRetryIdle stmt.ID = "scan.basic_accept.retry_idle"

func basicAcceptRetryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{{ID: basicAcceptRetryIdle, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_accept_retry.go"}, Kind: stmt.Write,
		SQL:     `UPDATE catalog_info_changes SET job_attempts=0,job_next_attempt_ms=? WHERE id=? AND asset_id=? AND asset_token=? AND expected_epoch=? AND input_revision=? AND fingerprint=? AND job_revision=? AND target_shape_id=? AND after_entity=? AND upper_entity=? AND slot=0 AND phase=5 AND family_mask=511 AND has_after=1 AND target_available=1 AND target_owner_object IS NULL AND target_owner_revision IS NULL AND old_path IS NULL AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' AND fault='' AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0 RETURNING id,job_revision,job_attempts,job_next_attempt_ms`,
		Params:  []stmt.Param{{Name: "cleared_next_attempt", Type: stmt.Integer, Nullable: true}, i("change_id"), i("asset_id"), {Name: "asset_token", Type: stmt.Text}, i("expected_epoch"), i("input_revision"), {Name: "fingerprint", Type: stmt.Blob}, i("job_revision"), i("shape_id"), i("after_entity"), i("upper_entity")},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
		Cohorts: []stmt.Cohort{{Name: "completed_first_input", Bindings: []any{nil, int64(51), int64(41), "basic-asset", int64(17), int64(7), bytes.Repeat([]byte{0x31}, 32), int64(15), int64(61), int64(31), int64(31)}}}}}
}
