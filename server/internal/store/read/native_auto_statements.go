package read

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Inert R21 base-table recipe. Actual plans are UNQUALIFIED; only parent
// Capture may establish execution authority. All raw seeks return before any
// acceptance filtering, so rejected positions remain visible and chargeable.
type nativeAutoStream struct {
	table, index, prefix, where string
	keys                        []string
	types                       []stmt.Type
}

var nativeAutoStreams = map[string]nativeAutoStream{
	"maps":        {"catalog_automatic_collections", "catalog_automatic_collection_entity", "entity_id", "", []string{"library_id", "provider", "provider_id"}, []stmt.Type{stmt.Integer, stmt.Text, stmt.Text}},
	"over":        {"catalog_automatic_member_overrides", "sqlite_autoindex_catalog_automatic_member_overrides_1", "collection_id", "", []string{"item_id"}, []stmt.Type{stmt.Integer}},
	"inverse":     {"catalog_automatic_member_overrides", "catalog_automatic_member_overrides_item", "item_id", "", []string{"collection_id"}, []stmt.Type{stmt.Integer}},
	"local":       {"catalog_entities", "catalog_entities_automatic_local", "library_id,automatic_collection_name_key", " AND a.kind=1 AND a.retired=0", []string{"id"}, []stmt.Type{stmt.Integer}},
	"evidence":    {"metadata_evidence_collections", "metadata_evidence_collection_members", "provider,collection_kind,collection_id", "", []string{"evidence_id"}, []stmt.Type{stmt.Text}},
	"origins":     {"metadata_field_origins", "metadata_field_origin_evidence", "evidence_id", "", []string{"entity_id", "field"}, []stmt.Type{stmt.Integer, stmt.Text}},
	"collections": {"metadata_evidence_collections", "sqlite_autoindex_metadata_evidence_collections_1", "evidence_id,collection_kind", "", []string{"collection_id"}, []stmt.Type{stmt.Text}},
}

func nativeAutomaticDefinitions() []stmt.Definition {
	var out []stmt.Definition
	auto := func(table string, cols ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk"}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return b
	}
	bindings := map[string]stmt.AutoIndexBinding{}
	for _, b := range []stmt.AutoIndexBinding{
		auto("catalog_automatic_member_overrides", "collection_id", "item_id"), auto("catalog_automatic_collections", "library_id", "provider", "provider_id"),
		auto("metadata_evidence_collections", "evidence_id", "collection_kind", "collection_id"), auto("metadata_field_origins", "entity_id", "field"),
		auto("metadata_subject_providers", "entity_id", "provider", "role"), auto("metadata_publications", "id"), auto("metadata_provider_operations", "id"), auto("metadata_provider_policy", "library_id", "provider"), auto("metadata_evidence", "id"), auto("libraries", "id"),
	} {
		bindings[b.Name] = b
	}
	add := func(name, sql string, types []stmt.Type, indexes, pk []string) {
		d := stmt.Definition{ID: stmt.ID("read.full.native.auto." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/native_auto.go"}, Plan: stmt.Plan{Hot: true, Indexes: indexes, PrimaryKeys: pk}}
		var values []any
		for n, t := range types {
			d.Params = append(d.Params, stmt.Param{Name: fmt.Sprintf("p%d", n), Type: t})
			if t == stmt.Text {
				values = append(values, "collection")
			} else {
				values = append(values, int64(1))
			}
		}

		// Actual native fixture coordinates; every stream has a separate
		// missing-owner/source cohort. This metadata is still not a receipt.
		switch {
		case strings.HasPrefix(name, "maps."):
			values[0] = int64(10)
			if len(values) > 1 {
				values[1] = int64(0)
			}
			if len(values) > 2 {
				values[1] = int64(1)
				values[2] = "a"
			}
			if len(values) > 3 {
				values[2] = "nfo"
				values[3] = "a"
			}
		case strings.HasPrefix(name, "over."):
			values[0] = int64(10)
			if len(values) > 1 {
				values[1] = int64(5)
			}
		case strings.HasPrefix(name, "inverse."):
			values[0] = int64(6)
			if len(values) > 1 {
				values[1] = int64(9)
			}
		case strings.HasPrefix(name, "local."):
			values[1] = "shared saga"
			if len(values) > 2 {
				values[2] = int64(4)
			}
		case strings.HasPrefix(name, "evidence."):
			values[0] = "tmdb"
			values[1] = "collection"
			values[2] = "provider"
			if len(values) > 3 {
				values[3] = "a"
			}
		case strings.HasPrefix(name, "origins."):
			values[0] = "evidence1"
			if len(values) > 1 {
				values[1] = int64(6)
			}
			if len(values) > 2 {
				values[1] = int64(7)
				values[2] = "a"
			}
		case strings.HasPrefix(name, "collections."):
			values[0] = "evidence1"
			values[1] = "collection"
			if len(values) > 2 {
				values[2] = "a"
			}
		case name == "work":
			values = []any{int64(5), int64(1)}
		case name == "owner":
			values = []any{int64(10), int64(1)}
		case name == "map":
			values = []any{int64(1), "nfo", "shared saga"}
		case name == "override":
			values = []any{int64(10), int64(6)}
		case name == "origin":
			values = []any{int64(7)}
		case name == "collection":
			values = []any{"evidence1", "provider"}
		case name == "accepted":
			values = []any{int64(7), int64(1), "tmdb", "evidence1"}
		}
		empty := append([]any(nil), values...)
		if _, ok := empty[0].(string); ok {
			empty[0] = "absent"
		} else {
			empty[0] = int64(999999)
		}
		d.Cohorts = []stmt.Cohort{{Name: "present", Bindings: values}, {Name: "empty", Bindings: empty}}

		for _, i := range indexes {
			if b, ok := bindings[i]; ok {
				d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, b)
			}
		}
		out = append(out, d)
	}
	names := []string{"maps", "over", "inverse", "local", "evidence", "origins", "collections"}
	for _, name := range names {
		s := nativeAutoStreams[name]
		prefix := strings.Split(s.prefix, ",")
		base := "SELECT " + "a." + strings.Join(s.keys, ",a.") + " FROM " + s.table + " AS a INDEXED BY " + s.index + " WHERE "
		var eq []string
		var ts []stmt.Type
		for _, k := range prefix {
			eq = append(eq, "a."+k+"=?")
			t := stmt.Text
			if k == "entity_id" || k == "collection_id" && name == "over" || k == "item_id" || k == "library_id" {
				t = stmt.Integer
			}
			ts = append(ts, t)
		}
		base += strings.Join(eq, " AND ") + s.where
		order := " ORDER BY a." + strings.Join(s.keys, ",a.") + " LIMIT 1"
		add(name+".first", base+order, ts, []string{s.index}, nil)
		for k := len(s.keys) - 1; k >= 0; k-- {
			tail := ""
			types := append([]stmt.Type(nil), ts...)
			for j := 0; j <= k; j++ {
				op := "="
				if j == k {
					op = ">"
				}
				tail += " AND a." + s.keys[j] + op + "?"
				types = append(types, s.types[j])
			}
			add(fmt.Sprintf("%s.tail%d", name, k), base+tail+order, types, []string{s.index}, nil)
		}
	}
	add("work", `SELECT a.automatic_collection_name_key FROM catalog_entities AS a WHERE a.id=? AND a.library_id=? AND a.kind=1 AND a.retired=0`, []stmt.Type{stmt.Integer, stmt.Integer}, nil, []string{"a"})
	add("owner", `SELECT a.id FROM catalog_entities AS a WHERE a.id=? AND a.library_id=? AND a.kind=10 AND a.retired=0`, []stmt.Type{stmt.Integer, stmt.Integer}, nil, []string{"a"})
	add("map", `SELECT a.entity_id,a.removed FROM catalog_automatic_collections AS a INDEXED BY sqlite_autoindex_catalog_automatic_collections_1 WHERE a.library_id=? AND a.provider=? AND a.provider_id=?`, []stmt.Type{stmt.Integer, stmt.Text, stmt.Text}, []string{"sqlite_autoindex_catalog_automatic_collections_1"}, nil)
	add("override", `SELECT a.present FROM catalog_automatic_member_overrides AS a INDEXED BY sqlite_autoindex_catalog_automatic_member_overrides_1 WHERE a.collection_id=? AND a.item_id=?`, []stmt.Type{stmt.Integer, stmt.Integer}, []string{"sqlite_autoindex_catalog_automatic_member_overrides_1"}, nil)
	add("origin", `SELECT a.provider,a.evidence_id FROM metadata_field_origins AS a INDEXED BY sqlite_autoindex_metadata_field_origins_1 WHERE a.entity_id=? AND a.field='relations'`, []stmt.Type{stmt.Integer}, []string{"sqlite_autoindex_metadata_field_origins_1"}, nil)
	add("collection", `SELECT a.provider,a.name FROM metadata_evidence_collections AS a INDEXED BY sqlite_autoindex_metadata_evidence_collections_1 WHERE a.evidence_id=? AND a.collection_kind='collection' AND a.collection_id=?`, []stmt.Type{stmt.Text, stmt.Text}, []string{"sqlite_autoindex_metadata_evidence_collections_1"}, nil)
	add("accepted", `SELECT e.id FROM catalog_entities AS e
 CROSS JOIN metadata_field_origins AS o INDEXED BY sqlite_autoindex_metadata_field_origins_1 ON o.entity_id=e.id
 CROSS JOIN catalog_libraries AS cl ON cl.id=e.library_id
 CROSS JOIN libraries AS lib INDEXED BY sqlite_autoindex_libraries_1 ON lib.id=cl.library_id
 CROSS JOIN metadata_subject_providers AS b INDEXED BY sqlite_autoindex_metadata_subject_providers_1 ON b.entity_id=e.id AND b.provider=o.provider
 CROSS JOIN metadata_publications AS p INDEXED BY sqlite_autoindex_metadata_publications_1 ON p.id=b.accepted_publication_id
 CROSS JOIN metadata_provider_operations AS op INDEXED BY sqlite_autoindex_metadata_provider_operations_1 ON op.id=p.operation_id
 CROSS JOIN metadata_provider_policy AS policy INDEXED BY sqlite_autoindex_metadata_provider_policy_1 ON policy.library_id=e.library_id AND policy.provider=o.provider
 CROSS JOIN metadata_evidence AS ev INDEXED BY sqlite_autoindex_metadata_evidence_1 ON ev.id=o.evidence_id
 WHERE e.id=? AND e.library_id=? AND o.provider=? AND o.evidence_id=?
 AND o.field='relations' AND b.role='primary' AND e.kind=1 AND e.retired=0 AND e.automatic_collection_name_key=''
 AND cl.retired=0 AND b.status='matched' AND b.accepted_evidence_id=o.evidence_id AND b.accepted_publication_id=o.publication_id
 AND p.entity_id=e.id AND p.evidence_id=o.evidence_id AND p.decision='accepted'
 AND op.entity_id=e.id AND op.provider=o.provider AND op.status='applied'
 AND policy.enabled=1 AND policy.incarnation=op.policy_incarnation AND policy.revision=op.policy_revision
 AND ev.sealed=1 AND ev.language=lib.metadata_language AND e.metadata_language=lib.metadata_language`, []stmt.Type{stmt.Integer, stmt.Integer, stmt.Text, stmt.Text}, []string{"sqlite_autoindex_libraries_1", "sqlite_autoindex_metadata_field_origins_1", "sqlite_autoindex_metadata_subject_providers_1", "sqlite_autoindex_metadata_publications_1", "sqlite_autoindex_metadata_provider_operations_1", "sqlite_autoindex_metadata_provider_policy_1", "sqlite_autoindex_metadata_evidence_1"}, []string{"e", "cl"})
	return out
}
