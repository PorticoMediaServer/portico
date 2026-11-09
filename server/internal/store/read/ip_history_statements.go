package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP owner/history statements. The owner point is the unique entity/person key;
// the history raw keyset is (kind,entity_id,id DESC), so later history rows never
// reorder an earlier page. Every variable text is measured in the same snapshot
// immediately before its payload is copied.
const ipOwnerSQL = `SELECT e.id,e.library_id,e.kind,e.metadata_incarnation,e.metadata_revision
FROM catalog_entities AS e
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

const ipOwnerPersonSQL = `SELECT p.id,p.artwork_revision
FROM catalog_people AS p
WHERE p.id=?`

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
const ipHistoryFieldsSQL = `SELECT f.history_id,f.field,f.before_value,f.before_value_present,f.before_automatic,f.before_automatic_present,f.before_locked,f.after_value,f.after_value_present,f.after_automatic,f.after_automatic_present,f.after_locked
FROM metadata_owner_history_fields AS f
WHERE f.history_id>=? AND f.history_id<=?
ORDER BY f.history_id,f.field LIMIT ?`

const ipHistoryFieldsOctetsSQL = `SELECT octet_length(f.field),octet_length(f.before_value),octet_length(f.before_automatic),octet_length(f.after_value),octet_length(f.after_automatic)
FROM metadata_owner_history_fields AS f
WHERE f.history_id>=? AND f.history_id<=?
ORDER BY f.history_id,f.field LIMIT ?`

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
	personCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}
	fieldParams := []stmt.Param{i("entity"), s("kind"), i("limit")}
	fieldCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(2001), "show", int64(33)}}, {Name: "empty", Bindings: []any{int64(999999), "show", int64(33)}}}
	personFieldParams := []stmt.Param{i("person"), i("limit")}
	personFieldCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(33)}}, {Name: "empty", Bindings: []any{int64(999999), int64(33)}}}
	historyParams := []stmt.Param{s("kind"), i("entity"), i("after"), i("limit")}
	historyCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"show", int64(2001), int64(1) << 62, int64(ipHistoryPageSize + 1)}}, {Name: "empty", Bindings: []any{"show", int64(999999), int64(1) << 62, int64(ipHistoryPageSize + 1)}}}
	fieldLimit := []stmt.Param{i("low"), i("high"), i("limit")}
	historyFieldCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1) << 62, int64(ipHistoryMaxFields + 1)}}, {Name: "empty", Bindings: []any{int64(1) << 62, int64(1) << 62, int64(ipHistoryMaxFields + 1)}}}

	ownerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}
	personPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"p"}, Seeks: []string{"rowid=?"}}
	ownerFactPlan := stmt.Plan{Hot: true, Indexes: []string{ownerKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{ownerKey}, Seeks: []string{"entity_id=?"}}
	ownerFactPersonPlan := stmt.Plan{Hot: true, Indexes: []string{personKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{personKey}, Seeks: []string{"person_id=?"}}
	fieldPlan := stmt.Plan{Hot: true, Indexes: []string{ownerKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{ownerKey}, Seeks: []string{"entity_id=?", "kind=? AND entity_id=?"}}
	personFieldPlan := stmt.Plan{Hot: true, Indexes: []string{personKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{personKey}, Seeks: []string{"person_id=?", "kind=? AND entity_id=?"}}
	historyPlan := stmt.Plan{Hot: true, Indexes: []string{"metadata_owner_history_entity"}, Seeks: []string{"kind=? AND entity_id=? AND id<?"}}
	historyFieldPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"f"}, Seeks: []string{"history_id>? AND history_id<?"}}

	return []stmt.Definition{
		{ID: "read.ip.owner", SQL: ipOwnerSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: ownerParams, Cohorts: ownerCohorts, Plan: ownerPlan},
		{ID: "read.ip.owner_person", SQL: ipOwnerPersonSQL, Kind: stmt.Read, Owner: "store/read IP history", CallSites: []string{"store/read/ip_history.go:ForegroundReader.IPHistory"}, Params: []stmt.Param{i("person")}, Cohorts: personCohorts, Plan: personPlan},
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
