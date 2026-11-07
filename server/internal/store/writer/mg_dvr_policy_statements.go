package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mgDVRPolicyDefaultsRead    stmt.ID = "writer.mg.dvr-policy.defaults-read"
	mgDVRPolicyDefaultsUpdate  stmt.ID = "writer.mg.dvr-policy.defaults-update"
	mgDVRPolicyStorageRead     stmt.ID = "writer.mg.dvr-policy.storage-read"
	mgDVRPolicyStorageUpdate   stmt.ID = "writer.mg.dvr-policy.storage-update"
	mgDVRPolicyDefaultsColumns         = `revision,before_seconds,after_seconds,retention_days,episode_limit,live_tuner_conflict,show_folder_template,movie_folder_template`
	mgDVRPolicyStorageColumns          = `revision,retention_days,episode_limit,floor_bytes,cap_bytes`
)

func mgDVRPolicyDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defaults := mgDVRDefaultsSnapshot{7, 60, 180, 0, 0, "keep-watching", "{series}/{title}", "{title} ({year})"}
	storage := mgDVRStorageSnapshot{11, mgDVRStorageDesired{}}
	defs := []stmt.Definition{
		{ID: mgDVRPolicyDefaultsRead, Kind: stmt.Read, SQL: `SELECT ` + mgDVRPolicyDefaultsColumns + ` FROM dvr_defaults WHERE singleton=?`, Params: []stmt.Param{i("singleton")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "missing", Bindings: []any{int64(2)}}}},
		{ID: mgDVRPolicyDefaultsUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_defaults SET revision=revision+1,before_seconds=?,after_seconds=?,retention_days=?,episode_limit=?,live_tuner_conflict=? WHERE singleton=? AND revision=? AND before_seconds=? AND after_seconds=? AND retention_days=? AND episode_limit=? AND live_tuner_conflict=? AND show_folder_template=? AND movie_folder_template=? RETURNING ` + mgDVRPolicyDefaultsColumns, Params: []stmt.Param{i("before"), i("after"), i("days"), i("episodes"), s("conflict"), i("singleton"), i("old_revision"), i("old_before"), i("old_after"), i("old_days"), i("old_episodes"), s("old_conflict"), s("old_show"), s("old_movie")}, Cohorts: []stmt.Cohort{{Name: "exact", Bindings: append([]any{int64(120), int64(300), int64(30), int64(0), "stop-watching", int64(1)}, defaults.values()...)}, {Name: "stale", Bindings: append([]any{int64(0), int64(0), int64(0), int64(0), "keep-watching", int64(1)}, mgDVRDefaultsSnapshot{6, 60, 180, 0, 0, "keep-watching", defaults.showTemplate, defaults.movieTemplate}.values()...)}}},
		{ID: mgDVRPolicyStorageRead, Kind: stmt.Read, SQL: `SELECT ` + mgDVRPolicyStorageColumns + ` FROM dvr_storage_policy WHERE singleton=?`, Params: []stmt.Param{i("singleton")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "missing", Bindings: []any{int64(2)}}}},
		{ID: mgDVRPolicyStorageUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_storage_policy SET revision=revision+1,retention_days=?,episode_limit=?,floor_bytes=?,cap_bytes=? WHERE singleton=? AND revision=? AND retention_days=? AND episode_limit=? AND floor_bytes=? AND cap_bytes=? RETURNING ` + mgDVRPolicyStorageColumns, Params: []stmt.Param{i("days"), i("episodes"), i("floor"), i("cap"), i("singleton"), i("old_revision"), i("old_days"), i("old_episodes"), i("old_floor"), i("old_cap")}, Cohorts: []stmt.Cohort{{Name: "exact", Bindings: append([]any{int64(30), int64(100), int64(1024), int64(8192), int64(1)}, storage.values()...)}, {Name: "stale", Bindings: append([]any{int64(0), int64(0), int64(0), int64(0), int64(1)}, mgDVRStorageSnapshot{10, storage.policy}.values()...)}}},
	}
	for k := range defs {
		d := &defs[k]
		d.Owner = "writer.mg.dvr-policy"
		d.CallSites = []string{"writer/mg_dvr_policy_effects.go"}
		table := "dvr_defaults"
		if d.ID == mgDVRPolicyStorageRead || d.ID == mgDVRPolicyStorageUpdate {
			table = "dvr_storage_policy"
		}
		d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{table}, Seeks: []string{"rowid=?"}}
	}
	return defs
}
func (s mgDVRDefaultsSnapshot) values() []any {
	return []any{s.revision, s.beforeSeconds, s.afterSeconds, s.retentionDays, s.episodeLimit, s.conflict, s.showTemplate, s.movieTemplate}
}
func (s mgDVRStorageSnapshot) values() []any { return append([]any{s.revision}, s.policy.values()...) }
func (s mgDVRStorageDesired) values() []any {
	return []any{s.retentionDays, s.episodeLimit, s.floorBytes, s.capBytes}
}
