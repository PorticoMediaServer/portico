package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicArtworkEntity    stmt.ID = "scan.basic_accept.artwork_entity"
	basicArtworkSubject   stmt.ID = "scan.basic_accept.artwork_subject"
	basicArtworkLink      stmt.ID = "scan.basic_accept.artwork_link"
	basicArtworkSelection stmt.ID = "scan.basic_accept.artwork_selection"
	basicArtworkJob       stmt.ID = "scan.basic_accept.artwork_job"
	basicArtworkInsert    stmt.ID = "scan.basic_accept.artwork_insert"
)

func basicArtworkDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicArtworkEntity, Kind: stmt.Read, SQL: `SELECT library_id,kind,retired,source_revision,artwork_revision,artwork_incarnation FROM catalog_entities WHERE id=?`, Params: []stmt.Param{i("entity_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "native_artwork_owner", Bindings: []any{int64(31)}}}},
		{ID: basicArtworkSubject, Kind: stmt.Read, SQL: `SELECT library_id,kind FROM catalog_subjects WHERE id=?`, Params: []stmt.Param{i("subject_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_subjects"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "retained_artwork_subject", Bindings: []any{int64(31)}}}},
		{ID: basicArtworkLink, Kind: stmt.Read, SQL: `SELECT library_id FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, Params: []stmt.Param{i("entity_id"), i("asset_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "same_library_asset_owner", Bindings: []any{int64(31), int64(41)}}}},
		{ID: basicArtworkSelection, Kind: stmt.Read, SQL: `SELECT revision FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice=?`, Params: []stmt.Param{t("kind"), i("entity_id"), t("role"), t("choice")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_selections"}, Seeks: []string{"kind=? AND entity_id=? AND role=? AND choice=?"}}, Cohorts: []stmt.Cohort{{Name: "chosen_matched_revision", Bindings: []any{"entity", int64(31), "poster", "matched"}}}},
		{ID: basicArtworkJob, Kind: stmt.Read, SQL: `SELECT id FROM artwork_jobs WHERE kind=? AND entity_id=? AND role=? AND choice=?`, Params: []stmt.Param{t("kind"), i("entity_id"), t("role"), t("choice")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_artwork_jobs_2"}, Seeks: []string{"kind=? AND entity_id=? AND role=? AND choice=?"}}, Cohorts: []stmt.Cohort{{Name: "chosen_job_absent", Bindings: []any{"entity", int64(31), "poster", "matched"}}}},
		{ID: basicArtworkInsert, Kind: stmt.Write, SQL: `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,locale,effective_language,source_object_id,source_object_revision,origin,provider,source_fence,selection_revision,status,error,actor,created_at,size_refresh,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id,kind,entity_id,role,choice,image_id,attribution,locale,effective_language,source_object_id,source_object_revision,origin,provider,source_fence,selection_revision,status,error,actor,created_at,size_refresh,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision`, Params: []stmt.Param{t("id"), t("kind"), i("entity_id"), t("role"), t("choice"), t("image_id"), t("attribution"), t("locale"), t("effective_language"), t("source_object_id"), t("source_object_revision"), t("origin"), t("provider"), t("source_fence"), i("selection_revision"), t("status"), t("error"), t("actor"), t("created_at"), i("size_refresh"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), i("attempts"), i("next_attempt_ms"), i("revision")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "chosen_local_acquisition", Bindings: []any{"local-job", "entity", int64(31), "poster", "matched", "chosen-image", "", "und", "en-US", "basic-object", "object:019", "local:chosen-image", "local", "native-owner-fence", int64(0), "pending", "", "", "1970-01-01T00:00:02Z", int64(0), int64(0), int64(0), int64(0), int64(0), int64(2000), int64(1)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_artwork.go"}
	}
	return defs
}
