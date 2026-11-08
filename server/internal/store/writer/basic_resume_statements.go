package writer

import (
	"bytes"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	basicResumeResetDue stmt.ID = "scan.basic_stage.resume_reset_due"
	basicResumeLengths  stmt.ID = "scan.basic_stage.resume_lengths"
	basicResumePhase    stmt.ID = "scan.basic_stage.resume_phase"
	basicResumeAsset    stmt.ID = "scan.basic_stage.resume_asset"
	basicResumeLink     stmt.ID = "scan.basic_stage.resume_link"
	basicResumeHeader   stmt.ID = "scan.basic_stage.resume_header"
	basicResumeTexts    stmt.ID = "scan.basic_stage.resume_texts"
	basicResumeReset    stmt.ID = "scan.basic_stage.resume_reset"
	basicResumeFinish   stmt.ID = "scan.basic_stage.resume_finish"
)

func basicResumeMaxUses(id stmt.ID) int {
	switch id {
	case basicResumeResetDue, basicResumeHeader, basicResumeTexts, basicResumeReset, basicResumeFinish, basicResumeLengths, basicResumePhase, basicResumeAsset, basicResumeLink:
		return 1
	}
	return 0
}

func basicResumeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}}
	identity := []stmt.Param{i("change_id"), i("asset_id"), t("asset_token"), i("expected_epoch"), i("input_revision"), b("fingerprint")}
	base := []any{int64(51), int64(41), "basic-asset", int64(17), int64(1), bytes.Repeat([]byte{0x31}, 32)}
	resetParams := append([]stmt.Param{{Name: "cleared_next_attempt_ms", Type: stmt.Integer, Nullable: true}, i("next_revision")}, identity...)
	resetParams = append(resetParams, i("revision"), i("phase"), i("shape"), i("entity"), i("upper_entity"), i("has_after"), i("copy_family"), i("copy_phase"), i("copy_id"), i("copy_aux"), t("copy_key"), t("copy_key2"), t("copy_key3"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), i("attempts"), t("fault"))
	resetBase := append([]any{nil, int64(5)}, base...)
	resetBase = append(resetBase, int64(5), int64(2), int64(61), int64(31), int64(31), int64(1), int64(2), int64(0), int64(0), int64(0), "", "", "", int64(13), int64(17), int64(1000), int64(1), "retry")
	defs := []stmt.Definition{
		{ID: basicResumeHeader, Kind: stmt.Read,
			SQL:    `SELECT phase,job_revision,after_entity,upper_entity,has_after,target_shape_id,copy_family,copy_phase,copy_id,copy_aux,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms FROM catalog_info_changes WHERE id=? AND asset_id=? AND asset_token=? AND slot=0 AND expected_epoch=? AND input_revision=? AND fingerprint=? AND family_mask=511 AND target_available=1 AND target_shape_id IS NOT NULL AND target_owner_object IS NULL AND target_owner_revision IS NULL`,
			Params: identity, Plan: point, Cohorts: []stmt.Cohort{{Name: "frozen_first", Bindings: base}}},
		{ID: basicResumeLengths, Kind: stmt.Read, SQL: `SELECT octet_length(copy_key),octet_length(copy_key2),octet_length(copy_key3),octet_length(fault) FROM catalog_info_changes WHERE id=? AND job_revision=?`, Params: []stmt.Param{i("change_id"), i("revision")}, Plan: point, Cohorts: []stmt.Cohort{{Name: "same_snapshot_lengths", Bindings: []any{int64(51), int64(5)}}}},
		{ID: basicResumePhase, Kind: stmt.Read, SQL: `SELECT phase FROM catalog_info_changes WHERE id=? AND job_revision=?`, Params: []stmt.Param{i("change_id"), i("revision")}, Plan: point, Cohorts: []stmt.Cohort{{Name: "after_copy", Bindings: []any{int64(51), int64(6)}}}},
		{ID: basicResumeAsset, Kind: stmt.Read, SQL: `SELECT info_publication_pending FROM catalog_assets WHERE id=? AND library_id=? AND info_epoch=?`, Params: []stmt.Param{i("asset_id"), i("library_id"), i("epoch")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "asset_fence", Bindings: []any{int64(41), int64(7), int64(17)}}}},
		{ID: basicResumeLink, Kind: stmt.Read, SQL: `SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`, Params: []stmt.Param{i("entity_id"), i("asset_id"), i("library_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "leaf_fence", Bindings: []any{int64(31), int64(41), int64(7)}}}},
		{ID: basicResumeTexts, Kind: stmt.Read, SQL: `SELECT copy_key,copy_key2,copy_key3,fault FROM catalog_info_changes WHERE id=? AND job_revision=?`, Params: []stmt.Param{i("change_id"), i("revision")}, Plan: point, Cohorts: []stmt.Cohort{{Name: "same_snapshot_texts", Bindings: []any{int64(51), int64(5)}}}},
		{ID: basicResumeReset, Kind: stmt.Write,
			SQL:    `UPDATE catalog_info_changes SET job_lease_owner=0,job_lease_generation=0,job_lease_expires_ms=0,job_next_attempt_ms=?,fault='',job_attempts=job_attempts+1,job_revision=? WHERE id=? AND asset_id=? AND asset_token=? AND expected_epoch=? AND input_revision=? AND fingerprint=? AND job_revision=? AND phase=? AND target_shape_id=? AND after_entity=? AND upper_entity=? AND has_after=? AND copy_family=? AND copy_phase=? AND copy_id=? AND copy_aux=? AND copy_key=? AND copy_key2=? AND copy_key3=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_next_attempt_ms IS NULL AND fault=? AND slot=0 AND family_mask=511 AND target_available=1 AND target_owner_object IS NULL AND target_owner_revision IS NULL RETURNING job_revision,job_attempts`,
			Params: resetParams, Plan: point, Cohorts: []stmt.Cohort{{Name: "expired_retry_reset", Bindings: resetBase}}},
		{ID: basicResumeFinish, Kind: stmt.Write,
			SQL:    `UPDATE catalog_info_changes SET job_attempts=0 WHERE id=? AND job_revision=? AND job_attempts=? AND phase=3 AND fault='' AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0 AND job_next_attempt_ms IS NULL RETURNING phase,job_revision,job_attempts`,
			Params: []stmt.Param{i("change_id"), i("revision"), i("attempts")}, Plan: point, Cohorts: []stmt.Cohort{{Name: "completed_copy_idle", Bindings: []any{int64(51), int64(6), int64(2)}}}},
	}
	// NULL and scheduled due values are distinct closed CAS shapes; the
	// general grammar intentionally admits no nullable-equality shortcut.
	for _, d := range defs {
		if d.ID == basicResumeReset {
			d.ID = basicResumeResetDue
			d.SQL = strings.Replace(d.SQL, "AND job_next_attempt_ms IS NULL AND fault=?", "AND job_next_attempt_ms=? AND fault=?", 1)
			d.Params = append(append(append([]stmt.Param{}, d.Params[:len(d.Params)-1]...), i("next_attempt_ms")), t("fault"))
			bindings := append(append([]any{}, resetBase[:len(resetBase)-1]...), int64(1), "retry")
			d.Cohorts = []stmt.Cohort{{Name: "expired_scheduled_retry_reset", Bindings: bindings}}
			defs = append(defs, d)
			break
		}
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_resume.go"}
	}
	return defs
}
