package read

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Member rows are consumed one raw (order_key,id) position at a time; the
// payload point is measured before decode. Personal state is a typed LEFT JOIN
// at the member's exact entity point. No directory union, pin position or
// global resource scan is part of this family.
const rsSavedMembersEntrySQL = `SELECT e.id,e.order_key,e.item_id FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
const rsSavedMembersEntryOctetsSQL = `SELECT octet_length(e.id),octet_length(e.order_key) FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
const rsSavedMembersEntryFirstSQL = `SELECT rowid FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=? ORDER BY order_key,id LIMIT ?`
const rsSavedMembersEntryTailSQL = `SELECT rowid FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=? AND order_key=? AND id>? ORDER BY order_key,id LIMIT ?`
const rsSavedMembersEntryNextSQL = `SELECT rowid FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=? AND order_key>? ORDER BY order_key,id LIMIT ?`
const rsSavedMembersShareRoleSQL = `SELECT role FROM saved_resource_shares INDEXED BY sqlite_autoindex_saved_resource_shares_1 WHERE resource_id=? AND authority='local' AND account_id=? AND profile_id=?`
const rsSavedMembersItemSQL = `SELECT id,library_id,kind,retired FROM catalog_entities WHERE id=?`
const rsSavedMembersCardSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.overview,p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed FROM catalog_entities AS e LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`
const rsSavedMembersCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview) FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	registerRSBytePoint("rs.saved.members.entry", rsBytePoint{lengths: "rs.saved.members.entry_octets", columns: []int{0, 1}})
	registerRSBytePoint("rs.saved.members.card", rsBytePoint{lengths: "rs.saved.members.card_octets", columns: []int{1, 2, 3, 8}})
}

// SavedCollectionPageDefinitions contains only this domain's bounded member
// positions and exact payload points. Rowids stay internal to a protected job;
// authenticated continuations contain logical order identities instead.
func SavedCollectionPageDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rs.saved.members." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_saved_page.go:SavedCollectionWork"}, Params: params, Cohorts: []stmt.Cohort{{Name: "source", Bindings: args}}, Plan: p})
	}
	for _, v := range []struct{ name, sql string }{{"entry", rsSavedMembersEntrySQL}, {"entry_octets", rsSavedMembersEntryOctetsSQL}} {
		add(v.name, v.sql, []stmt.Param{i("row"), s("resource")}, []any{int64(1), "saved"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	}
	add("entry.first", rsSavedMembersEntryFirstSQL, []stmt.Param{s("resource"), i("limit")}, []any{"saved", int64(2)}, stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}})
	add("entry.tail", rsSavedMembersEntryTailSQL, []stmt.Param{s("resource"), s("key"), s("after"), i("limit")}, []any{"saved", "", "", int64(2)}, stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}})
	add("entry.next", rsSavedMembersEntryNextSQL, []stmt.Param{s("resource"), s("key"), i("limit")}, []any{"saved", "", int64(2)}, stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}})
	sharePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resource_shares_1"}}
	sharePlan.AutoIndexes = rsSavedAutoIndexes(sharePlan.Indexes)
	add("share_role", rsSavedMembersShareRoleSQL, []stmt.Param{s("resource"), s("account"), s("profile")}, []any{"saved", "a", "p"}, sharePlan)
	add("item", rsSavedMembersItemSQL, []stmt.Param{i("item")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	for _, v := range []struct{ name, sql string }{{"card", rsSavedMembersCardSQL}, {"card_octets", rsSavedMembersCardOctetsSQL}} {
		p := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}
		if v.name == "card" {
			p.Indexes = []string{"personal_current_live"}
		}
		add(v.name, v.sql, []stmt.Param{s("profile"), i("item"), i("library"), i("kind")}, []any{"p", int64(1), int64(1), int64(1)}, p)
	}
	return out
}

// SavedCollectionHarnessDefinitions is explicit inert qualification composition,
// not a mutable registry or runtime default. Repeated exact dependencies have one
// ID; divergent definitions cannot be silently reconciled.
func SavedCollectionHarnessDefinitions() []stmt.Definition {
	var out []stmt.Definition
	seen := map[stmt.ID]stmt.Definition{}
	for _, group := range [][]stmt.Definition{browseDefinitions(), SavedCollectionDefinitions(), SavedCollectionPageDefinitions(), CollectionCompleteDefinitions(), rsDetailEligibilityDefinitions(), ReadingFactsDefinitions(), ArtworkDefinitions()} {
		for _, d := range group {
			if old, ok := seen[d.ID]; ok {
				if !reflect.DeepEqual(old, d) {
					panic("divergent saved member read dependency")
				}
				continue
			}
			seen[d.ID] = d
			out = append(out, d)
		}
	}
	return out
}
