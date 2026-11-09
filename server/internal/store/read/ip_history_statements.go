package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP owner/history statements. The owner point is the unique entity/person key;
// the history raw keyset is (kind,entity_id,id DESC), so later history rows never
// reorder an earlier page. Every variable text is measured in the same snapshot
// immediately before its payload is copied.
const ipOwnerSQL = `SELECT e.id,e.library_id,e.kind,e.metadata_incarnation,e.metadata_revision,e.admission_own_age
FROM catalog_entities AS e
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

// A person's descriptive history is readable only through the same visibility
// rule RS applies to person credits: a live credit whose work is visible and
// live in the admitted library/kind, whose current credit image is the credit's
// image and whose credit image is sealed for that exact owner revision. The
// order picks the same first credit RS's person page would read.
const ipOwnerPersonSQL = `SELECT p.id,p.artwork_revision,e.admission_own_age
FROM catalog_people AS p
CROSS JOIN catalog_credits AS c INDEXED BY catalog_credits_person ON c.person_id=p.id
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
LEFT JOIN catalog_credit_images AS h INDEXED BY catalog_credit_images_entity ON h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision AND h.id=c.image_id
WHERE p.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1
ORDER BY c.entity_id DESC,c.ord DESC,c.image_id DESC LIMIT 1`

const ipOwnerFactSQL = `SELECT own.id,own.revision,own.retired_ms
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_1
WHERE own.entity_id=? AND own.kind=?`

const ipOwnerFactPersonSQL = `SELECT own.id,own.revision,own.retired_ms
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_2
WHERE own.person_id=? AND own.kind=?`

const ipOwnerFieldsSQL = `SELECT f.field,f.value,f.automatic_value,f.value_present,f.automatic_present,f.locked,f.actor,f.observed_at
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_1
CROSS JOIN metadata_owner_fields AS f ON f.kind=own.kind AND f.entity_id=own.entity_id
WHERE own.entity_id=? AND own.kind=?
ORDER BY f.field LIMIT ?`

const ipOwnerFieldsOctetsSQL = `SELECT octet_length(f.field),octet_length(f.value),octet_length(f.automatic_value),octet_length(f.actor),octet_length(f.observed_at)
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_1
CROSS JOIN metadata_owner_fields AS f ON f.kind=own.kind AND f.entity_id=own.entity_id
WHERE own.entity_id=? AND own.kind=?
ORDER BY f.field LIMIT ?`

const ipOwnerFieldsPersonSQL = `SELECT f.field,f.value,f.automatic_value,f.value_present,f.automatic_present,f.locked,f.actor,f.observed_at
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_2
CROSS JOIN metadata_owner_fields AS f ON f.kind=own.kind AND f.entity_id=own.person_id
WHERE own.person_id=? AND own.kind='person'
ORDER BY f.field LIMIT ?`

const ipOwnerFieldsPersonOctetsSQL = `SELECT octet_length(f.field),octet_length(f.value),octet_length(f.automatic_value),octet_length(f.actor),octet_length(f.observed_at)
FROM metadata_edit_owners AS own INDEXED BY sqlite_autoindex_metadata_edit_owners_2
CROSS JOIN metadata_owner_fields AS f ON f.kind=own.kind AND f.entity_id=own.person_id
WHERE own.person_id=? AND own.kind='person'
ORDER BY f.field LIMIT ?`

// The raw keyset is strictly descending on history id, under the embedded
// (kind,entity_id,id DESC) index. A caller's after id excludes every row at or
// above it, including rows inserted after the earlier page was read.
const ipHistorySQL = `SELECT h.id,h.base_revision,h.revision,h.actor,h.observed_at
FROM metadata_owner_history AS h INDEXED BY metadata_owner_history_entity
WHERE h.kind=? AND h.entity_id=? AND h.id<? ORDER BY h.id DESC LIMIT ?`

const ipHistoryOctetsSQL = `SELECT octet_length(h.base_revision),octet_length(h.revision),octet_length(h.actor),octet_length(h.observed_at)
FROM metadata_owner_history AS h INDEXED BY metadata_owner_history_entity
WHERE h.kind=? AND h.entity_id=? AND h.id<? ORDER BY h.id DESC LIMIT ?`

// Typed history fields are the IP-24 record. The legacy JSON bodies are never
// projected: a bounded header plus bounded typed children is the whole ABI.
// The header is joined, not just ranged: metadata_owner_history.id is one
// global AUTOINCREMENT, so an owner's page spans ids that include other owners'
// edits. The join keeps the row cap and every field row inside this owner.
const ipHistoryFieldsSQL = `SELECT f.history_id,f.field,f.before_value,f.before_value_present,f.before_automatic,f.before_automatic_present,f.before_locked,f.after_value,f.after_value_present,f.after_automatic,f.after_automatic_present,f.after_locked
FROM metadata_owner_history AS h INDEXED BY metadata_owner_history_entity
CROSS JOIN metadata_owner_history_fields AS f ON f.history_id=h.id
WHERE h.kind=? AND h.entity_id=? AND h.id BETWEEN ? AND ?
ORDER BY h.id DESC,f.field LIMIT ?`

const ipHistoryFieldsOctetsSQL = `SELECT octet_length(f.field),octet_length(f.before_value),octet_length(f.before_automatic),octet_length(f.after_value),octet_length(f.after_automatic)
FROM metadata_owner_history AS h INDEXED BY metadata_owner_history_entity
CROSS JOIN metadata_owner_history_fields AS f ON f.history_id=h.id
WHERE h.kind=? AND h.entity_id=? AND h.id BETWEEN ? AND ?
ORDER BY h.id DESC,f.field LIMIT ?`

var ipOwnerFieldsVariable = []int{0, 1, 2, 6, 7}
var ipHistoryVariable = []int{1, 2, 3, 4}
var ipHistoryFieldsVariable = []int{1, 2, 4, 7, 9}

// Bounds on one history page and its typed children. Larger projections are
// refused, never truncated.
const (
	ipHistoryPageSize  = 8
	ipHistoryMaxFields = 256
	ipOwnerMaxFields   = 32
)

// IPHistoryHarnessDefinitions is inert harness composition for the private
// owner/history reads. Capture these exact definitions on the real reader before
// registration; nothing here is production wiring.
func IPHistoryHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	ownerKey := ipAutoKey("metadata_edit_owners", "sqlite_autoindex_metadata_edit_owners_1", "u", "entity_id")
	personKey := ipAutoKey("metadata_edit_owners", "sqlite_autoindex_metadata_edit_owners_2", "u", "person_id")

	ownerParams := []stmt.Param{i("entity"), i("library"), i("kind")}
	ownerCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(2001), int64(1), int64(2)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(2)}}}
	personParams := []stmt.Param{i("person"), i("library"), i("kind")}
	personCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}}
	fieldParams := []stmt.Param{i("entity"), s("kind"), i("limit")}
	fieldCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(2001), "show", int64(33)}}, {Name: "empty", Bindings: []any{int64(999999), "show", int64(33)}}}
	personFieldParams := []stmt.Param{i("person"), i("limit")}
	personFieldCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(33)}}, {Name: "empty", Bindings: []any{int64(999999), int64(33)}}}
	historyParams := []stmt.Param{s("kind"), i("entity"), i("after"), i("limit")}
	historyCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"show", int64(2001), int64(1) << 62, int64(ipHistoryPageSize + 1)}}, {Name: "empty", Bindings: []any{"show", int64(999999), int64(1) << 62, int64(ipHistoryPageSize + 1)}}}
	fieldLimit := []stmt.Param{s("kind"), i("entity"), i("low"), i("high"), i("limit")}
	historyFieldCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"show", int64(2001), int64(1), int64(1) << 62, int64(ipHistoryMaxFields + 1)}}, {Name: "empty", Bindings: []any{"show", int64(999999), int64(1), int64(1) << 62, int64(ipHistoryMaxFields + 1)}}}

	ownerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}
	personPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"p", "e"}, Indexes: []string{"catalog_credits_person", "catalog_credit_images_entity"}, Seeks: []string{"rowid=?", "person_id=?", "entity_id=?"},
		Waivers: []stmt.Waiver{{StatementID: "read.ip.owner_person", Stage: "eqp", Form: "BLOOM FILTER", Match: "BLOOM FILTER ON h (entity_id=?)", Reason: "after ANALYZE SQLite prefilters the already-forced catalog_credit_images_entity seek with a bloom filter on the credit's entity id; the filter only skips rows the join would reject, so it cannot widen or reorder the visible-credit predicate"}}}
	ownerFactPlan := stmt.Plan{Hot: true, Indexes: []string{ownerKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{ownerKey}, Seeks: []string{"entity_id=?"}}
	ownerFactPersonPlan := stmt.Plan{Hot: true, Indexes: []string{personKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{personKey}, Seeks: []string{"person_id=?"}}
	fieldPlan := stmt.Plan{Hot: true, Indexes: []string{ownerKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{ownerKey}, Seeks: []string{"entity_id=?", "kind=? AND entity_id=?"}}
	personFieldPlan := stmt.Plan{Hot: true, Indexes: []string{personKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{personKey}, Seeks: []string{"person_id=?", "kind=? AND entity_id=?"}}
	historyPlan := stmt.Plan{Hot: true, Indexes: []string{"metadata_owner_history_entity"}, Seeks: []string{"kind=? AND entity_id=? AND id<?"}}
	historyFieldPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"f"}, Indexes: []string{"metadata_owner_history_entity"}, Seeks: []string{"kind=? AND entity_id=? AND id>? AND id<?", "history_id=?"}}

	return []stmt.Definition{
		{ID: "read.ip.owner", SQL: ipOwnerSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: ownerParams, Cohorts: ownerCohorts, Plan: ownerPlan},
		{ID: "read.ip.owner_person", SQL: ipOwnerPersonSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: personParams, Cohorts: personCohorts, Plan: personPlan},
		{ID: "read.ip.owner_fact", SQL: ipOwnerFactSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: []stmt.Param{i("entity"), s("kind")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(2001), "show"}}, {Name: "empty", Bindings: []any{int64(999999), "show"}}}, Plan: ownerFactPlan},
		{ID: "read.ip.owner_fact_person", SQL: ipOwnerFactPersonSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: []stmt.Param{i("person"), s("kind")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), "person"}}, {Name: "empty", Bindings: []any{int64(999999), "person"}}}, Plan: ownerFactPersonPlan},
		{ID: "read.ip.owner_fields", SQL: ipOwnerFieldsSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: fieldParams, Cohorts: fieldCohorts, Plan: fieldPlan},
		{ID: "read.ip.owner_fields_bytes", SQL: ipOwnerFieldsOctetsSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: fieldParams, Cohorts: fieldCohorts, Plan: fieldPlan},
		{ID: "read.ip.owner_fields_person", SQL: ipOwnerFieldsPersonSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: personFieldParams, Cohorts: personFieldCohorts, Plan: personFieldPlan},
		{ID: "read.ip.owner_fields_person_bytes", SQL: ipOwnerFieldsPersonOctetsSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: personFieldParams, Cohorts: personFieldCohorts, Plan: personFieldPlan},
		{ID: "read.ip.history", SQL: ipHistorySQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: historyParams, Cohorts: historyCohorts, Plan: historyPlan},
		{ID: "read.ip.history_bytes", SQL: ipHistoryOctetsSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: historyParams, Cohorts: historyCohorts, Plan: historyPlan},
		{ID: "read.ip.history_fields", SQL: ipHistoryFieldsSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: fieldLimit, Cohorts: historyFieldCohorts, Plan: historyFieldPlan},
		{ID: "read.ip.history_fields_bytes", SQL: ipHistoryFieldsOctetsSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: fieldLimit, Cohorts: historyFieldCohorts, Plan: historyFieldPlan},
	}
}
