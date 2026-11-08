package read

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rsSavedPageResourceSQL = `SELECT r.id,r.name FROM saved_resources AS r WHERE r.rowid=?`
const rsSavedPageResourceOctetsSQL = `SELECT octet_length(r.id),octet_length(r.name) FROM saved_resources AS r WHERE r.rowid=?`
const rsSavedPageShareSQL = `SELECT s.resource_id FROM saved_resource_shares AS s WHERE s.rowid=?`
const rsSavedPageShareOctetsSQL = `SELECT octet_length(s.resource_id) FROM saved_resource_shares AS s WHERE s.rowid=?`
const rsSavedPageEntrySQL = `SELECT e.id,e.order_key,e.item_id FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
const rsSavedPageEntryOctetsSQL = `SELECT octet_length(e.id),octet_length(e.order_key) FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
const rsSavedPageMetadataSQL = `SELECT r.name,r.summary,r.created_at,r.revision,r.visibility FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0`
const rsSavedPageMetadataOctetsSQL = `SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.created_at),octet_length(r.visibility) FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0`
const rsSavedPageCardSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.overview,p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed FROM catalog_entities AS e LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`
const rsSavedPageCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview) FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	for _, v := range []struct {
		name    string
		columns []int
	}{{"resource", []int{0, 1}}, {"share_resource", []int{0}}, {"entry", []int{0, 1}}, {"metadata", []int{0, 1, 2, 4}}, {"card", []int{1, 2, 3, 8}}} {
		registerRSBytePoint("rs.saved_page."+v.name, rsBytePoint{lengths: "rs.saved_page." + v.name + "_octets", columns: v.columns})
	}
}

// SavedCollectionPageDefinitions contains only this domain's bounded native
// positions and exact payload points. Rowids remain internal to a protected job;
// authenticated continuations contain logical source/order identities instead.
func SavedCollectionPageDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rs.saved_page." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_saved_page.go:SavedCollectionWork"}, Params: params, Cohorts: []stmt.Cohort{{Name: "source", Bindings: args}}, Plan: p})
	}
	for _, stream := range []struct {
		name, index, where string
		params             []stmt.Param
		args               []any
	}{
		{"owner", "saved_resource_owner", `owner_key=? AND kind='collection' AND deleted=0`, []stmt.Param{text("owner")}, []any{"owner"}},
		{"server", "saved_resource_visibility", `kind='collection' AND visibility='server' AND deleted=0`, nil, nil},
	} {
		base := `SELECT rowid FROM saved_resources INDEXED BY ` + stream.index + ` WHERE ` + stream.where
		order := ` ORDER BY name COLLATE NOCASE,id LIMIT 1`
		p := stmt.Plan{Hot: true, Indexes: []string{stream.index}}
		add(stream.name+".first", base+order, stream.params, stream.args, p)
		add(stream.name+".tail", base+` AND name COLLATE NOCASE=? AND id>?`+order, append(append([]stmt.Param(nil), stream.params...), text("name"), text("after")), append(append([]any(nil), stream.args...), "", ""), p)
		add(stream.name+".next", base+` AND name COLLATE NOCASE>?`+order, append(append([]stmt.Param(nil), stream.params...), text("name")), append(append([]any(nil), stream.args...), ""), p)
	}
	add("shared", `SELECT rowid FROM saved_resource_shares INDEXED BY saved_resource_shared WHERE authority='local' AND account_id=? AND profile_id=? AND resource_id>? ORDER BY resource_id LIMIT 1`, []stmt.Param{text("account"), text("profile"), text("after")}, []any{"a", "p", ""}, stmt.Plan{Hot: true, Indexes: []string{"saved_resource_shared"}})
	for _, v := range []struct {
		name, sql string
		params    []stmt.Param
		args      []any
		p         stmt.Plan
	}{
		{"resource", rsSavedPageResourceSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r"}}},
		{"resource_octets", rsSavedPageResourceOctetsSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r"}}},
		{"share_resource", rsSavedPageShareSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s"}}},
		{"share_resource_octets", rsSavedPageShareOctetsSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s"}}},
		{"entry", rsSavedPageEntrySQL, []stmt.Param{integer("row"), text("resource")}, []any{int64(1), "saved"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{"entry_octets", rsSavedPageEntryOctetsSQL, []stmt.Param{integer("row"), text("resource")}, []any{int64(1), "saved"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{"metadata", rsSavedPageMetadataSQL, []stmt.Param{text("resource")}, []any{"saved"}, rsSavedPagePK("saved_resources", []string{"id"})},
		{"metadata_octets", rsSavedPageMetadataOctetsSQL, []stmt.Param{text("resource")}, []any{"saved"}, rsSavedPagePK("saved_resources", []string{"id"})},
	} {
		add(v.name, v.sql, v.params, v.args, v.p)
	}
	base := `SELECT rowid FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=?`
	p := stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}}
	add("entry.first", base+` ORDER BY order_key,id LIMIT 1`, []stmt.Param{text("resource")}, []any{"saved"}, p)
	add("entry.tail", base+` AND order_key=? AND id>? ORDER BY order_key,id LIMIT 1`, []stmt.Param{text("resource"), text("key"), text("after")}, []any{"saved", "", ""}, p)
	add("entry.next", base+` AND order_key>? ORDER BY order_key,id LIMIT 1`, []stmt.Param{text("resource"), text("key")}, []any{"saved", ""}, p)
	add("pin", `SELECT pinned,revision FROM saved_pins INDEXED BY sqlite_autoindex_saved_pins_1 WHERE owner_key=? AND kind='collection' AND resource_id=?`, []stmt.Param{text("owner"), text("resource")}, []any{"owner", "saved"}, rsSavedPagePK("saved_pins", []string{"owner_key", "kind", "resource_id"}))
	add("position", `SELECT position FROM saved_pin_order INDEXED BY sqlite_autoindex_saved_pin_order_1 WHERE owner_key=? AND kind='collection' AND resource_id=?`, []stmt.Param{text("owner"), text("resource")}, []any{"owner", "saved"}, rsSavedPagePK("saved_pin_order", []string{"owner_key", "kind", "resource_id"}))
	add("share_role", `SELECT role FROM saved_resource_shares INDEXED BY sqlite_autoindex_saved_resource_shares_1 WHERE resource_id=? AND authority='local' AND account_id=? AND profile_id=?`, []stmt.Param{text("resource"), text("account"), text("profile")}, []any{"saved", "a", "p"}, rsSavedPagePK("saved_resource_shares", []string{"resource_id", "authority", "account_id", "profile_id"}))
	add("item", `SELECT id,library_id,kind,retired FROM catalog_entities WHERE id=?`, []stmt.Param{integer("item")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	for _, v := range []struct{ name, sql string }{{"card", rsSavedPageCardSQL}, {"card_octets", rsSavedPageCardOctetsSQL}} {
		p := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}
		if v.name == "card" {
			p.Indexes = []string{"personal_current_live"}
		}
		add(v.name, v.sql, []stmt.Param{text("profile"), integer("item"), integer("library"), integer("kind")}, []any{"p", int64(1), int64(1), int64(1)}, p)
	}
	return out
}
func rsSavedPagePK(table string, columns []string) stmt.Plan {
	index := "sqlite_autoindex_" + table + "_1"
	pin := stmt.AutoIndexBinding{Table: table, Name: index, Origin: "pk"}
	for _, c := range columns {
		pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
	}
	return stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{pin}}
}

// SavedCollectionHarnessDefinitions is explicit inert qualification composition,
// not a mutable registry or runtime default. Repeated exact dependencies have
// one ID; divergent definitions cannot be silently reconciled.
func SavedCollectionHarnessDefinitions() []stmt.Definition {
	var out []stmt.Definition
	seen := map[stmt.ID]stmt.Definition{}
	for _, group := range [][]stmt.Definition{browseDefinitions(), SavedCollectionDefinitions(), SavedCollectionPageDefinitions(), rsDetailEligibilityDefinitions(), ReadingFactsDefinitions(), AutomaticCollectionDefinitions(), ArtworkDefinitions()} {
		for _, d := range group {
			if old, ok := seen[d.ID]; ok {
				if !reflect.DeepEqual(old, d) {
					panic("divergent saved read dependency")
				}
				continue
			}
			seen[d.ID] = d
			out = append(out, d)
		}
	}
	return out
}
