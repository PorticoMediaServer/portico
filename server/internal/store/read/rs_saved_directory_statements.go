package read

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The saved COLLECTION directory is an inert harness family until the phase 4
// adapter composes it. It owns its own bounded positions and exact payload
// points: the authorized owner/server/share union, the viewer's pin facts, the
// resource metadata and the per-resource member order used for exact counts and
// four samples. It never borrows the saved member page's statement family.
//
// Streams use the canonical persisted indexes (saved_resource_owner,
// saved_resource_visibility, saved_resource_shared). The viewer's pin position
// point uses saved_pin_order_position and the pin facts use the saved pin PK.
const (
	rsSavedDirectoryOwnerFirstSQL = `SELECT r.rowid FROM saved_resources AS r INDEXED BY saved_resource_owner WHERE r.owner_key=? AND r.kind='collection' AND r.deleted=0 ORDER BY r.name COLLATE NOCASE,r.id LIMIT ?`
	rsSavedDirectoryOwnerTailSQL  = `SELECT r.rowid FROM saved_resources AS r INDEXED BY saved_resource_owner WHERE r.owner_key=? AND r.kind='collection' AND r.deleted=0 AND r.name COLLATE NOCASE=? AND r.id>? ORDER BY r.name COLLATE NOCASE,r.id LIMIT ?`
	rsSavedDirectoryOwnerNextSQL  = `SELECT r.rowid FROM saved_resources AS r INDEXED BY saved_resource_owner WHERE r.owner_key=? AND r.kind='collection' AND r.deleted=0 AND r.name COLLATE NOCASE>? ORDER BY r.name COLLATE NOCASE,r.id LIMIT ?`

	rsSavedDirectoryServerFirstSQL = `SELECT r.rowid FROM saved_resources AS r INDEXED BY saved_resource_visibility WHERE r.kind='collection' AND r.visibility='server' AND r.deleted=0 ORDER BY r.name COLLATE NOCASE,r.id LIMIT ?`
	rsSavedDirectoryServerTailSQL  = `SELECT r.rowid FROM saved_resources AS r INDEXED BY saved_resource_visibility WHERE r.kind='collection' AND r.visibility='server' AND r.deleted=0 AND r.name COLLATE NOCASE=? AND r.id>? ORDER BY r.name COLLATE NOCASE,r.id LIMIT ?`
	rsSavedDirectoryServerNextSQL  = `SELECT r.rowid FROM saved_resources AS r INDEXED BY saved_resource_visibility WHERE r.kind='collection' AND r.visibility='server' AND r.deleted=0 AND r.name COLLATE NOCASE>? ORDER BY r.name COLLATE NOCASE,r.id LIMIT ?`

	rsSavedDirectoryShareSQL = `SELECT s.rowid FROM saved_resource_shares AS s INDEXED BY saved_resource_shared WHERE s.authority='local' AND s.account_id=? AND s.profile_id=? AND s.resource_id>? ORDER BY s.resource_id LIMIT ?`

	rsSavedDirectoryResourceSQL       = `SELECT r.id,r.name FROM saved_resources AS r WHERE r.rowid=? AND r.kind='collection' AND r.deleted=0`
	rsSavedDirectoryResourceOctetsSQL = `SELECT octet_length(r.id),octet_length(r.name) FROM saved_resources AS r WHERE r.rowid=? AND r.kind='collection' AND r.deleted=0`
	rsSavedDirectoryShareResourceSQL  = `SELECT s.resource_id FROM saved_resource_shares AS s WHERE s.rowid=?`
	rsSavedDirectoryShareResOctetSQL  = `SELECT octet_length(s.resource_id) FROM saved_resource_shares AS s WHERE s.rowid=?`

	rsSavedDirectoryPermissionSourceSQL = `SELECT r.revision,r.deleted FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=r.id WHERE t.token_hash=? AND r.id=? AND f.authority='local' AND r.kind='collection' AND r.deleted=0 AND r.owner_authority=f.authority AND r.owner_account=f.account_id AND r.owner_profile=f.profile_id LIMIT 1`
	rsSavedDirectoryPermissionServerSQL = `SELECT r.revision,r.deleted FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0 AND r.visibility='server' LIMIT 1`
	rsSavedDirectoryPermissionShareSQL  = `SELECT r.revision,r.deleted FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=r.id CROSS JOIN saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 ON s.resource_id=r.id AND s.authority=f.authority AND s.account_id=f.account_id AND s.profile_id=f.profile_id WHERE t.token_hash=? AND r.id=? AND f.authority='local' AND r.kind='collection' AND r.deleted=0 LIMIT 1`
	rsSavedDirectoryShareRoleSQL        = `SELECT s.role FROM saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 WHERE s.resource_id=? AND s.authority='local' AND s.account_id=? AND s.profile_id=?`

	rsSavedDirectoryMetadataSQL       = `SELECT r.name,r.summary,r.created_at,r.revision,r.visibility FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0`
	rsSavedDirectoryMetadataOctetsSQL = `SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.created_at),octet_length(r.visibility) FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0`

	rsSavedDirectoryGenerationSQL = `SELECT revision FROM saved_resource_generation WHERE id=1`
	rsSavedDirectoryPinOrderSQL   = `SELECT revision FROM saved_pin_order_revisions INDEXED BY sqlite_autoindex_saved_pin_order_revisions_1 WHERE owner_key=?`
	rsSavedDirectoryPinSQL        = `SELECT pinned,revision FROM saved_pins INDEXED BY sqlite_autoindex_saved_pins_1 WHERE owner_key=? AND kind='collection' AND resource_id=?`
	rsSavedDirectoryPositionSQL   = `SELECT position FROM saved_pin_order INDEXED BY saved_pin_order_position WHERE owner_key=? AND kind='collection' AND resource_id=?`

	rsSavedDirectoryItemSQL = `SELECT id,library_id,kind,retired FROM catalog_entities WHERE id=?`

	rsSavedDirectoryEntrySQL       = `SELECT e.id,e.order_key,e.item_id FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
	rsSavedDirectoryEntryOctetsSQL = `SELECT octet_length(e.id),octet_length(e.order_key) FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
	rsSavedDirectoryEntryFirstSQL  = `SELECT e.rowid FROM saved_resource_entries AS e INDEXED BY saved_resource_entries_order WHERE e.resource_id=? ORDER BY e.order_key,e.id LIMIT ?`
	rsSavedDirectoryEntryTailSQL   = `SELECT e.rowid FROM saved_resource_entries AS e INDEXED BY saved_resource_entries_order WHERE e.resource_id=? AND e.order_key=? AND e.id>? ORDER BY e.order_key,e.id LIMIT ?`
	rsSavedDirectoryEntryNextSQL   = `SELECT e.rowid FROM saved_resource_entries AS e INDEXED BY saved_resource_entries_order WHERE e.resource_id=? AND e.order_key>? ORDER BY e.order_key,e.id LIMIT ?`

	rsSavedDirectoryCardSQL       = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.overview,p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed FROM catalog_entities AS e LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`
	rsSavedDirectoryCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview) FROM catalog_entities AS e LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`
)

func init() {
	for _, v := range []struct {
		name    string
		columns []int
	}{
		{"resource", []int{0, 1}},
		{"share_resource", []int{0}},
		{"metadata", []int{0, 1, 2, 4}},
		{"entry", []int{0, 1}},
		{"card", []int{1, 2, 3, 8}},
	} {
		registerRSBytePoint("rs.saved.directory."+v.name, rsBytePoint{lengths: "rs.saved.directory." + v.name + "_octets", columns: v.columns})
	}
}

// SavedDirectoryDefinitions is inert: composition must Capture these exact
// definitions on the actual Reader. No runtime route is registered here.
func SavedDirectoryDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(site, name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rs.saved.directory." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_saved_directory.go:" + site}, Params: params, Cohorts: []stmt.Cohort{{Name: "source", Bindings: args}}, Plan: p})
	}
	// Authorized union streams. Each step reads one raw position under its own
	// cap+1 window; the raw continuation is (name,id) or (resource_id) and is
	// never a public cursor.
	owner := stmt.Plan{Hot: true, Indexes: []string{"saved_resource_owner"}}
	add("SavedCollectionDirectoryWork", "owner.first", rsSavedDirectoryOwnerFirstSQL, []stmt.Param{text("owner"), integer("limit")}, []any{"owner", int64(2)}, owner)
	add("SavedCollectionDirectoryWork", "owner.tail", rsSavedDirectoryOwnerTailSQL, []stmt.Param{text("owner"), text("name"), text("after"), integer("limit")}, []any{"owner", "", "", int64(2)}, owner)
	add("SavedCollectionDirectoryWork", "owner.next", rsSavedDirectoryOwnerNextSQL, []stmt.Param{text("owner"), text("name"), integer("limit")}, []any{"owner", "", int64(2)}, owner)
	server := stmt.Plan{Hot: true, Indexes: []string{"saved_resource_visibility"}}
	add("SavedCollectionDirectoryWork", "server.first", rsSavedDirectoryServerFirstSQL, []stmt.Param{integer("limit")}, []any{int64(2)}, server)
	add("SavedCollectionDirectoryWork", "server.tail", rsSavedDirectoryServerTailSQL, []stmt.Param{text("name"), text("after"), integer("limit")}, []any{"", "", int64(2)}, server)
	add("SavedCollectionDirectoryWork", "server.next", rsSavedDirectoryServerNextSQL, []stmt.Param{text("name"), integer("limit")}, []any{"", int64(2)}, server)
	add("SavedCollectionDirectoryWork", "share", rsSavedDirectoryShareSQL, []stmt.Param{text("account"), text("profile"), text("after"), integer("limit")}, []any{"a", "p", "", int64(2)}, stmt.Plan{Hot: true, Indexes: []string{"saved_resource_shared"}})

	for _, v := range []struct {
		name, sql string
		params    []stmt.Param
		args      []any
		p         stmt.Plan
	}{
		{"resource", rsSavedDirectoryResourceSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r"}}},
		{"resource_octets", rsSavedDirectoryResourceOctetsSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r"}}},
		{"share_resource", rsSavedDirectoryShareResourceSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s"}}},
		{"share_resource_octets", rsSavedDirectoryShareResOctetSQL, []stmt.Param{integer("row")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s"}}},
		{"entry", rsSavedDirectoryEntrySQL, []stmt.Param{integer("row"), text("resource")}, []any{int64(1), "saved"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{"entry_octets", rsSavedDirectoryEntryOctetsSQL, []stmt.Param{integer("row"), text("resource")}, []any{int64(1), "saved"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
	} {
		add("SavedCollectionDirectoryWork", v.name, v.sql, v.params, v.args, v.p)
	}

	generation := stmt.Plan{Hot: true, PrimaryKeys: []string{"saved_resource_generation"}}
	add("SavedCollectionDirectoryWork", "generation", rsSavedDirectoryGenerationSQL, nil, nil, generation)

	pinOrder := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_pin_order_revisions_1"}}
	pinOrder.AutoIndexes = rsSavedDirectoryAutoIndexes(pinOrder.Indexes)
	add("SavedCollectionDirectoryWork", "pin_order", rsSavedDirectoryPinOrderSQL, []stmt.Param{text("owner")}, []any{"owner"}, pinOrder)

	pin := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_pins_1"}}
	pin.AutoIndexes = rsSavedDirectoryAutoIndexes(pin.Indexes)
	add("SavedCollectionDirectoryWork", "pin", rsSavedDirectoryPinSQL, []stmt.Param{text("owner"), text("resource")}, []any{"owner", "saved"}, pin)

	// The exact viewer arrangement point reads the canonical persisted
	// (owner_key,position,resource_id) index. It returns at most the one row
	// selected by the saved pin PK; it never lists the viewer's full order.
	add("SavedCollectionDirectoryWork", "position", rsSavedDirectoryPositionSQL, []stmt.Param{text("owner"), text("resource")}, []any{"owner", "saved"}, stmt.Plan{Hot: true, Indexes: []string{"saved_pin_order_position"}})

	// Permission points are this family's own: a missing resource, a VIEW
	// resource, a deleted row or another principal's private collection is not
	// permitted. Each is a bounded existence point; the delivery proof rechecks
	// all three in the same final snapshot.
	sourcePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_saved_resources_1"}}
	sourcePlan.AutoIndexes = rsSavedDirectoryAutoIndexes(sourcePlan.Indexes)
	add("rsSavedDirectoryPermission", "permission.source", rsSavedDirectoryPermissionSourceSQL, []stmt.Param{text("token"), text("resource")}, []any{"token", "saved"}, sourcePlan)
	serverPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}}
	serverPlan.AutoIndexes = rsSavedDirectoryAutoIndexes(serverPlan.Indexes)
	add("rsSavedDirectoryPermission", "permission.server", rsSavedDirectoryPermissionServerSQL, []stmt.Param{text("resource")}, []any{"saved"}, serverPlan)
	sharePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_saved_resources_1", "sqlite_autoindex_saved_resource_shares_1"}}
	sharePlan.AutoIndexes = rsSavedDirectoryAutoIndexes(sharePlan.Indexes)
	add("rsSavedDirectoryPermission", "permission.share", rsSavedDirectoryPermissionShareSQL, []stmt.Param{text("token"), text("resource")}, []any{"token", "saved"}, sharePlan)
	rolePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resource_shares_1"}}
	rolePlan.AutoIndexes = rsSavedDirectoryAutoIndexes(rolePlan.Indexes)
	add("rsSavedDirectoryPermission", "permission.share_role", rsSavedDirectoryShareRoleSQL, []stmt.Param{text("resource"), text("account"), text("profile")}, []any{"saved", "a", "p"}, rolePlan)

	metadataPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}}
	metadataPlan.AutoIndexes = rsSavedDirectoryAutoIndexes(metadataPlan.Indexes)
	add("SavedCollectionDirectoryWork", "metadata", rsSavedDirectoryMetadataSQL, []stmt.Param{text("resource")}, []any{"saved"}, metadataPlan)
	add("SavedCollectionDirectoryWork", "metadata_octets", rsSavedDirectoryMetadataOctetsSQL, []stmt.Param{text("resource")}, []any{"saved"}, metadataPlan)

	add("SavedCollectionDirectoryWork", "item", rsSavedDirectoryItemSQL, []stmt.Param{integer("item")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})

	entryOrder := stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}}
	add("SavedCollectionDirectoryWork", "entry.first", rsSavedDirectoryEntryFirstSQL, []stmt.Param{text("resource"), integer("limit")}, []any{"saved", int64(2)}, entryOrder)
	add("SavedCollectionDirectoryWork", "entry.tail", rsSavedDirectoryEntryTailSQL, []stmt.Param{text("resource"), text("key"), text("after"), integer("limit")}, []any{"saved", "", "", int64(2)}, entryOrder)
	add("SavedCollectionDirectoryWork", "entry.next", rsSavedDirectoryEntryNextSQL, []stmt.Param{text("resource"), text("key"), integer("limit")}, []any{"saved", "", int64(2)}, entryOrder)
	for _, v := range []struct{ name, sql string }{{"card", rsSavedDirectoryCardSQL}, {"card_octets", rsSavedDirectoryCardOctetsSQL}} {
		cardPlan := stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, PrimaryKeys: []string{"e"}}
		add("SavedCollectionDirectoryWork", v.name, v.sql, []stmt.Param{text("profile"), integer("item"), integer("library"), integer("kind")}, []any{"p", int64(1), int64(1), int64(1)}, cardPlan)
	}
	return out
}

// rsSavedDirectoryAutoIndexes names the primary-key index shape for each forced
// generated index. Capture still proves the actual plan and native binding.
func rsSavedDirectoryAutoIndexes(indexes []string) []stmt.AutoIndexBinding {
	var out []stmt.AutoIndexBinding
	for _, idx := range indexes {
		var table string
		var columns []string
		switch idx {
		case "sqlite_autoindex_saved_resources_1":
			table, columns = "saved_resources", []string{"id"}
		case "sqlite_autoindex_saved_resource_shares_1":
			table, columns = "saved_resource_shares", []string{"resource_id", "authority", "account_id", "profile_id"}
		case "sqlite_autoindex_authorization_family_tokens_1":
			table, columns = "authorization_family_tokens", []string{"token_hash"}
		case "sqlite_autoindex_authorization_session_families_1":
			table, columns = "authorization_session_families", []string{"id"}
		case "sqlite_autoindex_saved_pins_1":
			table, columns = "saved_pins", []string{"owner_key", "kind", "resource_id"}
		case "sqlite_autoindex_saved_pin_order_revisions_1":
			table, columns = "saved_pin_order_revisions", []string{"owner_key"}
		default:
			continue
		}
		pin := stmt.AutoIndexBinding{Table: table, Name: idx, Origin: "pk"}
		for _, c := range columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		out = append(out, pin)
	}
	return out
}

// SavedCollectionDirectoryHarnessDefinitions is the explicit inert qualification
// composition for the directory. It includes only the exact dependencies the
// directory reads: the common fence, the authored collection reducer used to
// refuse an automatic member until its separately owned reducer lands, detail
// subject eligibility, Reading facts and page art. Repeated exact dependencies
// keep one definition; divergent ones cannot be silently reconciled.
func SavedCollectionDirectoryHarnessDefinitions() []stmt.Definition {
	var out []stmt.Definition
	seen := map[stmt.ID]stmt.Definition{}
	for _, group := range [][]stmt.Definition{browseDefinitions(), SavedDirectoryDefinitions(), CollectionCompleteDefinitions(), rsDetailEligibilityDefinitions(), ReadingFactsDefinitions(), ArtworkDefinitions()} {
		for _, d := range group {
			if old, ok := seen[d.ID]; ok {
				if !reflect.DeepEqual(old, d) {
					panic("divergent saved directory read dependency")
				}
				continue
			}
			seen[d.ID] = d
			out = append(out, d)
		}
	}
	return out
}
