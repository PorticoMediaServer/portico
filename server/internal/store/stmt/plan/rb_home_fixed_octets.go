package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"strings"
)

// Inert, own definitions only. Composition must Capture its actual Reader.
func rbHomeFixedExpectedBindingDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name string, cols ...string) stmt.AutoIndexBinding {
		k := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range cols {
			k.Columns = append(k.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return k
	}
	lib := key("libraries", "sqlite_autoindex_libraries_1", "id")
	src := key("library_sources", "sqlite_autoindex_library_sources_1", "id")
	add := func(name, sql string, p []stmt.Param, args, empty []any, plan stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: stmt.ID("read.browse.rb.home.binding." + name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_binding.go:readHomeWholePlayableBinding"}, SQL: sql, Params: p, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: plan}
	}
	return []stmt.Definition{
		add("item", `SELECT e.retired,e.public_id,e.metadata_incarnation,e.playback_origin_incarnation,e.playback_origin_revision,e.metadata_revision,e.source_revision,e.identity_revision,cl.library_id,gone.item_id,recording.item_id,length(NULLIF(cl.root,l.root)),l.age_pending_operations,l.age_input_revision,l.age_published_revision,l.playback_origin_incarnation,l.playback_origin_revision,l.data_revision,l.data_metadata_revision,e.kind,e.revision
 FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind AND k.playable=1
 CROSS JOIN catalog_libraries AS cl ON cl.id=e.library_id AND cl.retired=0
 CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=cl.library_id
 LEFT JOIN dvr_catalog_retirements AS gone ON gone.item_id=e.id
 LEFT JOIN dvr_catalog_provenance AS recording ON recording.item_id=e.id
 WHERE e.id=? AND e.library_id=?`, []stmt.Param{i("item"), i("library")}, []any{int64(1), int64(1)}, []any{int64(999999), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "k", "cl", "gone", "recording"}, Indexes: []string{lib.Name}, AutoIndexes: []stmt.AutoIndexBinding{lib}}),
		add("links", `SELECT l.asset_id,l.part_index,l.start_seconds,l.end_seconds,l.info_pending_change,a.info_publication_pending,a.available,a.playback_origin_incarnation,a.playback_origin_revision,l.playback_origin_incarnation,l.playback_origin_revision,a.prepared_source_revision,a.duration,l.info_duration,a.token,l.library_id,l.available,a.size,a.modified_ns
 FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order
 LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id AND a.library_id=?
 WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`, []stmt.Param{i("library"), i("item"), i("limit")}, []any{int64(1), int64(1), int64(2)}, []any{int64(1), int64(999999), int64(2)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}}),
		add("objects", `SELECT o.id,o.revision,o.source_id,s.incarnation,o.retired,length(NULLIF(o.state,'available')),length(NULLIF(o.unsupported_reason,'')),length(NULLIF(o.root_incarnation,s.incarnation)),s.enabled,length(NULLIF(s.health,'root_changed')),length(NULLIF(s.health,'removing')),s.generation,length(NULLIF(s.library_id,?)),o.size,o.modified_ns,s.root,s.configured_root,s.identity_confirmed
 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object
 LEFT JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id
 WHERE o.asset_id=? ORDER BY o.id LIMIT ?`, []stmt.Param{s("library"), s("asset"), i("limit")}, []any{"movies", "home-1", int64(64)}, []any{"movies", "missing", int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object", src.Name}, AutoIndexes: []stmt.AutoIndexBinding{src}, Seeks: []string{"asset_id=?"}}),
	}
}

func init() {
	for n, original := range rbHomeFixedExpectedBindingDefinitions() {
		fields := []string{"octet_length(cl.library_id)", "octet_length(a.token)", "octet_length(NULLIF(s.library_id,?)),octet_length(o.id),octet_length(o.revision),octet_length(o.source_id),octet_length(s.incarnation),octet_length(s.root),octet_length(s.configured_root)"}
		sqlText := "SELECT " + fields[n] + original.SQL[strings.Index(original.SQL, "\n FROM "):]
		name := []string{"item", "links", "objects"}[n]
		expected := original
		expected.ID = stmt.ID("read.browse.rb.home.fixed.binding." + name + ".bytes")
		expected.SQL = sqlText
		if n == 0 {
			expected.SQL = strings.Replace(expected.SQL, "catalog_entities AS e", "catalog_entities AS e INDEXED BY catalog_entities_library_id", 1)
			sqlText = expected.SQL
		}
		if n == 0 {
			expected.Plan.PrimaryKeys = []string{"k", "cl"}
			expected.Plan.Indexes = append(expected.Plan.Indexes, "catalog_entities_library_id")
			expected.Plan.Seeks = []string{"library_id=? AND id=?"}
		}
		pointOctets.add(octetShape{id: expected.ID, sql: sqlText, mismatch: "exact bounded Home binding byte range required",
			definition: func(d stmt.Definition) bool {
				return d.ID == expected.ID && d.SQL == expected.SQL && d.Kind == expected.Kind && d.Owner == expected.Owner && reflect.DeepEqual(d.Params, expected.Params) && reflect.DeepEqual(d.Plan, expected.Plan)
			},
			audit: func(ctx context.Context, c *sql.Conn) error {
				if e := auditHomeFixedRowID(ctx, c, "catalog_entities"); e != nil {
					return e
				}
				if e := auditHomeFixedRowID(ctx, c, "catalog_assets"); e != nil {
					return e
				}
				return auditClosedOctetKey(ctx, c, "library_sources", "sqlite_autoindex_library_sources_1", []string{"id"})
			},
			point: func(eqp Rows) error {
				for _, v := range eqp.Values {
					if len(v) != 4 || !strings.HasPrefix(v[3].(string), "SEARCH ") {
						return fmt.Errorf("Home binding byte preflight requires SEARCH-only plan")
					}
				}
				return nil
			},
		})
	}
}

func auditHomeFixedRowID(ctx context.Context, c *sql.Conn, table string) error {
	tables, e := query(ctx, c, "PRAGMA main.table_list("+identifier(table)+")")
	if e != nil {
		return e
	}
	if len(tables.Values) != 1 || tables.Values[0][0] != "main" || tables.Values[0][2] != "table" || tables.Values[0][4] != int64(0) {
		return fmt.Errorf("Home fixed bytes require ordinary rowid table")
	}
	cols, e := query(ctx, c, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if e != nil {
		return e
	}
	found := false
	for _, v := range cols.Values {
		if v[5] != int64(0) {
			if found || v[1] != "id" || v[2] != "INTEGER" || v[5] != int64(1) || v[6] != int64(0) {
				return fmt.Errorf("Home fixed bytes require exact INTEGER primary key")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("Home fixed bytes require primary key")
	}
	return nil
}

const homeFixedPersonalBytesSQL = `SELECT octet_length(activity_at),octet_length(container_watermark),octet_length(container_cleared_through),octet_length(dismissed_activity_at),octet_length(resume_playback_id),octet_length(dismissed_playback_id) FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=?`

func init() {
	expected := stmt.Definition{ID: "read.browse.rb.home.fixed.personal.bytes", Owner: "store/read", Kind: stmt.Read, SQL: homeFixedPersonalBytesSQL, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "item", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, Seeks: []string{"profile_id=? AND item_id=?"}}}
	pointOctets.add(octetShape{id: expected.ID, sql: expected.SQL, mismatch: "exact native personal Home byte point required", definition: func(d stmt.Definition) bool {
		return d.ID == expected.ID && d.SQL == expected.SQL && d.Owner == expected.Owner && d.Kind == expected.Kind && reflect.DeepEqual(d.Params, expected.Params) && reflect.DeepEqual(d.Plan, expected.Plan)
	}, audit: func(ctx context.Context, c *sql.Conn) error {
		if e := auditClosedOctetKey(ctx, c, "library_sources", "sqlite_autoindex_library_sources_1", []string{"id"}); e != nil {
			return e
		}
		rows, e := query(ctx, c, "PRAGMA main.index_list(personal_items)")
		if e != nil {
			return e
		}
		found := false
		for _, v := range rows.Values {
			if v[1] == "personal_current_live" {
				if v[2] != int64(1) || v[4] != int64(0) {
					return fmt.Errorf("Home personal bytes require complete unique point")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("Home personal native point missing")
		}
		cols, e := query(ctx, c, "PRAGMA main.index_info(personal_current_live)")
		if e != nil {
			return e
		}
		if len(cols.Values) != 2 || cols.Values[0][2] != "profile_id" || cols.Values[1][2] != "item_id" {
			return fmt.Errorf("Home personal byte point key mismatch")
		}
		return nil
	}, point: func(rows Rows) error {
		if len(rows.Values) != 1 || rows.Values[0][3] != "SEARCH personal_items USING INDEX personal_current_live (profile_id=? AND item_id=?)" {
			return fmt.Errorf("Home personal exact unique SEARCH required")
		}
		return nil
	}})
}
