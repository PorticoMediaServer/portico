package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const uoBirthJobNative stmt.ID = "uo.birth.job.native"

// One exact prepared-job point read. It pins the born row PK/worker through the
// MP command-owner index, the complete public preparedmedia.Job field set, the
// historical user root/owner lifetimes and the independent worker's zero
// counters. The item relation uses read/mp_prepared.go's source pins: the
// catalogue entity must be the job's native item, carry the selected public id,
// kind and library, and not be retired. This statement scans no job family and
// reads no mutable head.
func uoBirthJobDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	return []stmt.Definition{{ID: uoBirthJobNative, Kind: stmt.Read, Owner: "store/writer UO exact 740 birth original", CallSites: []string{"store/writer/uo_birth_job.go"},
		SQL: `SELECT e.public_id FROM prepared_media_jobs AS j INDEXED BY mp_job_command_owner
CROSS JOIN catalog_entities AS e ON e.id=j.item_id
WHERE j.id=? AND j.item_id=? AND e.public_id=? AND e.kind=? AND e.library_id=? AND j.library_id=? AND e.retired=0 AND j.source_revision=?
AND j.asset_id=? AND j.profile_id=? AND j.target_id=? AND j.state=? AND j.phase=? AND j.generation=? AND j.bytes=? AND j.error_code=? AND j.revision=? AND j.created_ms=? AND j.updated_ms=? AND j.version_id=?
AND j.mp_command_incarnation=? AND j.mp_user_root_id=? AND j.mp_owner_member_incarnation=? AND j.mp_owner_profile_lifetime=?
AND j.mp_command_last_sequence=0 AND j.mp_command_acknowledged_sequence=0 AND j.mp_owner_revision=1 AND j.mp_receipt_rows=0 AND j.mp_receipt_variable_bytes=0`,
		Params:  []stmt.Param{t("job"), i("item"), b("public"), i("kind"), i("library"), t("library_key"), t("source_revision"), t("asset"), t("profile"), t("target"), t("state"), t("phase"), i("generation"), i("bytes"), t("error_code"), i("revision"), i("created"), i("updated"), t("version"), b("worker"), t("root"), t("member"), t("lifetime")},
		Cohorts: []stmt.Cohort{{Name: "unqualified_exact_birth_shape", Bindings: []any{"job-original", int64(2111), []byte{3, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(7), "library-one", "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "original-asset", "portable-720p-high-v3", "prepared-library", "queued", "waiting", int64(0), int64(0), "", int64(1), int64(1762641000000), int64(1762641000000), "", []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "abcdefghijklmnopqrstuv", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}},

		Plan: stmt.Plan{Hot: true, Indexes: []string{"mp_job_command_owner"}, Seeks: []string{"id=? AND mp_command_incarnation=?"}, PrimaryKeys: []string{"e"}},
	}}
}
