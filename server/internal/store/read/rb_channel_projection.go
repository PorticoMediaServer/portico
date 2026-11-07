package read

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rbChannelProjectionRaw = 65

// The projection uses no ORDER BY, SQL aggregate or unbounded prefilter. Every
// link/object (including rejected ones) consumes raw work. A full65-row result
// is unresolved, never a complete source-state proof. Canonical Go row digests
// preserve the complete bounded multiset without depending on planner order.
func rbChannelRowsDigest(rows [][]any) ([32]byte, error) {
	var out [32]byte
	if len(rows) > rbChannelProjectionRaw {
		return out, ErrNotReady
	}
	var digests [rbChannelProjectionRaw][32]byte
	for i, row := range rows {
		h := sha256.New()
		integer := func(v uint64) { var b [8]byte; binary.LittleEndian.PutUint64(b[:], v); _, _ = h.Write(b[:]) }
		integer(uint64(len(row)))
		for _, cell := range row {
			switch v := cell.(type) {
			case nil:
				_, _ = h.Write([]byte{0})
			case int64:
				_, _ = h.Write([]byte{1})
				integer(uint64(v))
			case float64:
				_, _ = h.Write([]byte{2})
				integer(math.Float64bits(v))
			case string:
				_, _ = h.Write([]byte{3})
				integer(uint64(len(v)))
				_, _ = h.Write([]byte(v))
			case []byte:
				_, _ = h.Write([]byte{4})
				integer(uint64(len(v)))
				_, _ = h.Write(v)
			default:
				return out, ErrUnqualified
			}
		}
		copy(digests[i][:], h.Sum(nil))
	}
	sort.Slice(digests[:len(rows)], func(i, j int) bool { return bytes.Compare(digests[i][:], digests[j][:]) < 0 })
	h := sha256.New()
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(len(rows)))
	_, _ = h.Write(count[:])
	for _, d := range digests[:len(rows)] {
		_, _ = h.Write(d[:])
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}

const rbChannelMetaColumns = `e.id,e.public_id,e.title,e.revision,e.retired,e.year,e.show_id,e.season_id,e.kind,e.episode_number,e.numbering,e.release_date,e.release_key,e.added_at,e.sort_key,e.sort_key_keep_articles,e.playback_origin_incarnation,e.playback_origin_revision,e.source_revision,e.metadata_incarnation,sh.public_id,sh.title,sh.retired,sh.year,sh.kind,se.id,se.season_number,se.retired,se.show_id`
const rbChannelParents = ` LEFT JOIN catalog_entities AS sh NOT INDEXED ON sh.id=e.show_id AND sh.library_id=e.library_id AND sh.kind=2 LEFT JOIN catalog_entities AS se NOT INDEXED ON se.id=e.season_id AND se.library_id=e.library_id AND se.kind=3`

func rbChannelCandidateDefinitions() []stmt.Definition {
	i := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		p := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, c := range columns {
			p.Columns = append(p.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return p
	}
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present, empty []any, policy stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rb.channel." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_channel_window.go:SharedChannelReader.SharedChannelWindow"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: policy})
	}
	add("candidates", "SELECT "+rbChannelMetaColumns+" FROM catalog_entities AS e INDEXED BY catalog_entities_library"+rbChannelParents+" WHERE e.library_id=? AND e.kind=? AND e.id>? ORDER BY e.id LIMIT ?", []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(0), int64(rbChannelRaw)}, []any{int64(999999), int64(1), int64(0), int64(rbChannelRaw)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, PrimaryKeys: []string{"sh", "se"}, Seeks: []string{"library_id=? AND kind=? AND id>?"}})
	add("item", "SELECT "+rbChannelMetaColumns+" FROM catalog_entities AS e"+rbChannelParents+" WHERE e.id=? AND e.library_id=? AND e.kind=?", []stmt.Param{i("item"), i("library"), i("kind")}, []any{int64(1), int64(1), int64(1)}, []any{int64(999999), int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "sh", "se"}})
	source := key("library_sources", "sqlite_autoindex_library_sources_1", "pk", "id")
	library := key("libraries", "sqlite_autoindex_libraries_1", "pk", "id")
	boundary := key("episode_asset_boundaries", "sqlite_autoindex_episode_asset_boundaries_1", "pk", "item_id", "asset_id")
	asset := key("catalog_assets", "sqlite_autoindex_catalog_assets_2", "u", "library_id", "id")
	kind := key("catalog_kinds", "sqlite_autoindex_catalog_kinds_1", "u", "name")
	bridge := key("catalog_libraries", "sqlite_autoindex_catalog_libraries_1", "u", "library_id")
	projection := `SELECT ` + rbChannelMetaColumns + `,a.token,a.path,a.container,a.size,a.modified_ns,a.playback_origin_revision,a.prepared_source_revision,a.available,a.info_publication_pending,l.asset_id,l.library_id,l.part_index,l.available,l.start_seconds,l.end_seconds,l.info_pending_change,l.playback_origin_incarnation,l.playback_origin_revision,l.info_duration,a.playback_origin_incarnation,a.duration,o.id,o.revision,o.source_id,o.asset_id,o.root_incarnation,o.retired,length(NULLIF(o.state,'available')),length(NULLIF(o.unsupported_reason,'')),o.size,o.modified_ns,s.id,s.library_id,s.incarnation,s.generation,s.root,s.configured_root,s.enabled,s.identity_confirmed,length(NULLIF(s.health,'root_changed')),length(NULLIF(s.health,'removing')),gone.item_id,recording.item_id,b.status,k.playable,cl.retired,length(NULLIF(cl.root,lib.root)),lib.age_pending_operations,lib.age_input_revision,lib.age_published_revision,lib.playback_origin_incarnation,lib.playback_origin_revision,lib.data_revision,lib.data_metadata_revision,e.metadata_revision,e.identity_revision,e.admission_revision,e.aggregate_revision
FROM catalog_entities AS e` + rbChannelParents + `
LEFT JOIN catalog_asset_links AS l INDEXED BY catalog_asset_links_order ON l.entity_id=e.id
LEFT JOIN catalog_assets AS a INDEXED BY sqlite_autoindex_catalog_assets_2 ON a.id=l.asset_id AND a.library_id=e.library_id
LEFT JOIN inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object ON o.asset_id=a.token
LEFT JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id
LEFT JOIN dvr_catalog_retirements AS gone ON gone.item_id=e.id
LEFT JOIN dvr_catalog_provenance AS recording ON recording.item_id=e.id
LEFT JOIN episode_asset_boundaries AS b INDEXED BY sqlite_autoindex_episode_asset_boundaries_1 ON b.item_id=e.id AND b.asset_id=a.token
LEFT JOIN catalog_kinds AS k INDEXED BY sqlite_autoindex_catalog_kinds_1 ON k.name=? AND k.id=e.kind
LEFT JOIN catalog_libraries AS cl INDEXED BY sqlite_autoindex_catalog_libraries_1 ON cl.library_id=? AND cl.id=e.library_id
LEFT JOIN libraries AS lib INDEXED BY sqlite_autoindex_libraries_1 ON lib.id=cl.library_id
WHERE e.id=? AND e.library_id=? AND e.kind=? LIMIT ?`
	add("check", projection, []stmt.Param{text("kind_name"), text("library_name"), i("item"), i("library"), i("kind"), i("limit")}, []any{"movie", "movies", int64(1), int64(1), int64(1), int64(rbChannelProjectionRaw)}, []any{"movie", "movies", int64(999999), int64(1), int64(1), int64(rbChannelProjectionRaw)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "sh", "se", "gone", "recording"}, Indexes: []string{"catalog_asset_links_order", "inventory_objects_reading_asset_object", source.Name, boundary.Name, library.Name, asset.Name, kind.Name, bridge.Name}, AutoIndexes: []stmt.AutoIndexBinding{source, boundary, library, asset, kind, bridge}, Seeks: []string{"entity_id=?", "asset_id=?"}})
	return out
}
