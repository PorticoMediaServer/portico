package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// MP reading resume display is a closed set of point reads over the caller's
// current personal row and that item's latest saved source version. The child is
// chosen by personal_reading_resumes_latest order BEFORE any completion or
// current-source filter, so an older unfinished version can never replace a
// newer completed or stale one. No personal row is written here.
const mpReadingResumePersonalSQL = `SELECT i.id,i.item_public_id,i.revision,i.reading_revision FROM personal_items AS i INDEXED BY personal_current_live WHERE i.profile_id=? AND i.item_id=?`

// The index itself supplies (observed_ms DESC,asset_id DESC), so the latest
// version is one SEARCH with no temp B-tree and no window over the title's
// history. Only fixed-width columns are selected before the byte preflight.
const mpReadingResumeLatestSQL = `SELECT r.asset_id,r.observed_ms,r.revision,r.progression FROM personal_reading_resumes AS r INDEXED BY personal_reading_resumes_latest WHERE r.personal_item_id=? ORDER BY r.observed_ms DESC,r.asset_id DESC LIMIT 1`

// The selected child is read by its complete native key, and its variable bytes
// are charged before the payload is copied in the same snapshot. plan's domain
// registry owns the closed octet shape and its EQP audit; the read plans test
// asserts the same native index shape on the exact draft DDL.
const mpReadingResumeChildOctetsSQL = `SELECT octet_length(r.source_token),octet_length(r.source_revision),octet_length(r.format),octet_length(r.cfi),octet_length(r.section) FROM personal_reading_resumes AS r INDEXED BY sqlite_autoindex_personal_reading_resumes_1 WHERE r.personal_item_id=? AND r.asset_id=?`
const mpReadingResumeChildSQL = `SELECT r.source_token,r.source_revision,r.format,r.cfi,r.section,r.page_index,r.progression,r.revision,r.observed_ms FROM personal_reading_resumes AS r INDEXED BY sqlite_autoindex_personal_reading_resumes_1 WHERE r.personal_item_id=? AND r.asset_id=?`

// Variable payload columns, in the exact order of the child octet preflight.
var mpReadingResumeChildVariable = []int{0, 1, 2, 3, 4}

var (
	mpReadingResumePersonalIndex = "personal_current_live"
	mpReadingResumeLatestIndex   = "personal_reading_resumes_latest"
	mpReadingResumeChildIndex    = stmt.AutoIndexBinding{Table: "personal_reading_resumes", Name: "sqlite_autoindex_personal_reading_resumes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "personal_item_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}
)

func init() {
	registerRSBytePoint("mp.reading.resume.child", rsBytePoint{lengths: "mp.reading.resume.child_octets", columns: mpReadingResumeChildVariable})
}

// MPReadingResumeDefinitions is the closed statement recipe for the resume
// display. The item/liveness point and the current source selection are the
// typed decoder MP-01 already owns; this file adds only personal and child reads.
func MPReadingResumeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	call := []string{"store/read/mp_reading_resume.go:MPReadingResumeReader.Select"}

	personalParams := []stmt.Param{text("profile"), i("item")}
	childParams := []stmt.Param{i("personal"), i("asset")}

	personalCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"p", int64(1101)}}, {Name: "empty", Bindings: []any{"missing", int64(1101)}}}
	latestCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(9001)}}, {Name: "empty", Bindings: []any{int64(999999)}}}
	childCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(9001), int64(201)}}, {Name: "empty", Bindings: []any{int64(999999), int64(201)}}}

	personalPlan := stmt.Plan{Hot: true, Indexes: []string{mpReadingResumePersonalIndex}, Seeks: []string{"profile_id=? AND item_id=?"}}
	latestPlan := stmt.Plan{Hot: true, Indexes: []string{mpReadingResumeLatestIndex}, Seeks: []string{"personal_item_id=?"}}
	childPlan := stmt.Plan{Hot: true, Indexes: []string{mpReadingResumeChildIndex.Name}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingResumeChildIndex}, Seeks: []string{"personal_item_id=? AND asset_id=?"}}

	return []stmt.Definition{
		{ID: readStatementID("mp.reading.resume.personal"), SQL: mpReadingResumePersonalSQL, Kind: stmt.Read, Owner: "store/read MP", CallSites: call, Params: personalParams, Cohorts: personalCohorts, Plan: personalPlan},
		{ID: readStatementID("mp.reading.resume.latest"), SQL: mpReadingResumeLatestSQL, Kind: stmt.Read, Owner: "store/read MP", CallSites: call, Params: []stmt.Param{i("personal")}, Cohorts: latestCohorts, Plan: latestPlan},
		{ID: readStatementID("mp.reading.resume.child"), SQL: mpReadingResumeChildSQL, Kind: stmt.Read, Owner: "store/read MP", CallSites: call, Params: childParams, Cohorts: childCohorts, Plan: childPlan},
		{ID: readStatementID("mp.reading.resume.child_octets"), SQL: mpReadingResumeChildOctetsSQL, Kind: stmt.Read, Owner: "store/read MP", CallSites: call, Params: childParams, Cohorts: childCohorts, Plan: childPlan},
	}
}

// MPReadingResumeHarnessDefinitions composes the shared read fences, the MP-01
// typed source decoder and this task's closed recipe. It is a Capture input,
// never a runtime grant.
func MPReadingResumeHarnessDefinitions() []stmt.Definition {
	return append(append(HarnessDefinitions(), MPReadingSourceDefinitions()...), MPReadingResumeDefinitions()...)
}
