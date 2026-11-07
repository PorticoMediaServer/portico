package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func mpPreparedDefinitions() []stmt.Definition {
	job := stmt.AutoIndexBinding{Table: "prepared_media_jobs", Name: "sqlite_autoindex_prepared_media_jobs_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	return []stmt.Definition{{ID: readStatementID("foreground.mp.prepared_job"), Owner: "store/read MP", CallSites: []string{"store/read/mp_prepared.go:ForegroundReader.PreparedJob"}, Kind: stmt.Read,
		SQL: `SELECT j.id,j.state,j.phase,j.generation,j.cleaned_generation,j.bytes,j.error_code,j.revision,j.created_ms,j.updated_ms,j.version_id,j.asset_id,j.target_id,j.profile_id,j.source_revision,
e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,
j.library_id,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms
FROM prepared_media_jobs AS j INDEXED BY sqlite_autoindex_prepared_media_jobs_1
CROSS JOIN catalog_entities AS e ON e.id=j.item_id
WHERE j.id=? AND j.item_id=? AND e.library_id=? AND e.kind=? AND j.library_id=? AND e.retired=0`,
		Params:  []stmt.Param{{Name: "job", Type: stmt.Text}, {Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}, {Name: "library_name", Type: stmt.Text}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"mp-job", int64(1), int64(1), int64(1), "movies"}}, {Name: "empty", Bindings: []any{"missing", int64(1), int64(1), int64(1), "movies"}}},
		Plan:    stmt.Plan{Hot: true, Indexes: []string{job.Name}, AutoIndexes: []stmt.AutoIndexBinding{job}, PrimaryKeys: []string{"e"}},
	}}
}
