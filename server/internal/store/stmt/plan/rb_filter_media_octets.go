package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The media filter kernel reserves actual variable bytes before hydration. Only
// these exact compiled point forms may call octet_length; each contribution
// owns its shape, native key audit and EQP row check.
const rbFilterMediaAttributionOctetsSQL = `SELECT octet_length(e.reading_author_value),octet_length(e.reading_author_key),octet_length(e.reading_series_value),octet_length(e.reading_series_key),octet_length(e.reading_series_id) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=?`
const rbFilterMediaLinksOctetsSQL = `SELECT l.asset_id,octet_length(a.token) FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id AND a.library_id=? WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`
const rbFilterMediaAssetOctetsSQL = `SELECT octet_length(a.token),octet_length(a.path) FROM catalog_assets AS a WHERE a.id=?`
const rbFilterMediaSourcesOctetsSQL = `SELECT octet_length(id),octet_length(root),octet_length(configured_root),octet_length(health),octet_length(incarnation) FROM library_sources INDEXED BY library_sources_library WHERE library_id=? ORDER BY id LIMIT ?`
const rbFilterMediaStreamsOctetsSQL = `SELECT s.stream_index,octet_length(s.language),octet_length(s.detail_json) FROM asset_streams AS s WHERE s.asset_id=? AND s.stream_index>? ORDER BY s.stream_index LIMIT ?`
const rbFilterMediaSubtitlesOctetsSQL = `SELECT t.id,octet_length(t.language) FROM asset_subtitles AS t INDEXED BY asset_subtitles_asset WHERE t.asset_id=? AND t.id>? ORDER BY t.id LIMIT ?`
const rbFilterMediaSubtitleResourcesOctetsSQL = `SELECT octet_length(rev.language) FROM subtitle_resources AS r INDEXED BY subtitle_resources_item INNER JOIN subtitle_revisions AS rev ON rev.resource_id=r.id AND rev.revision=r.current_revision INNER JOIN catalog_assets AS a ON a.token=r.source_id AND a.size=rev.source_size AND a.modified_ns=rev.source_modified_ns WHERE r.item_id=? AND r.deleted=0 LIMIT ?`
const rbFilterMediaPublicationOctetsSQL = `SELECT p.asset_id,octet_length(p.format) FROM reading_publications AS p WHERE p.item_id=? AND p.asset_id>? ORDER BY p.asset_id LIMIT ?`

func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	assetPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Seeks: []string{"rowid=?"}}
	linkPlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, PrimaryKeys: []string{"a"}, Seeks: []string{"entity_id=?", "rowid=?"}}
	shapes := []struct {
		id, sql string
		params  []stmt.Param
		plan    stmt.Plan
		audit   func(context.Context, *sql.Conn) error
		rows    []string
	}{
		{"attribution.bytes", rbFilterMediaAttributionOctetsSQL, []stmt.Param{i("item"), i("library")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}, auditRSDetailOctetPoint, []string{"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)"}},
		{"links.bytes", rbFilterMediaLinksOctetsSQL, []stmt.Param{i("library"), i("item"), i("limit")}, linkPlan, func(ctx context.Context, c *sql.Conn) error {
			if err := auditClosedOctetKey(ctx, c, "catalog_asset_links", "", []string{"entity_id", "asset_id"}); err != nil {
				return err
			}
			if err := rbFilterMediaIndexAudit(ctx, c, "catalog_asset_links", "catalog_asset_links_order", "c", []string{"entity_id", "part_index", "asset_id"}); err != nil {
				return err
			}
			return rbFilterMediaIntegerRowid(ctx, c, "catalog_assets")
		}, []string{"SEARCH l USING INDEX catalog_asset_links_order (entity_id=?)", "SEARCH a USING INTEGER PRIMARY KEY (rowid=?) LEFT-JOIN"}},
		{"asset.bytes", rbFilterMediaAssetOctetsSQL, []stmt.Param{i("asset")}, assetPlan, func(ctx context.Context, c *sql.Conn) error {
			if err := rbFilterMediaOctetFunction(ctx, c); err != nil {
				return err
			}
			if err := rbFilterMediaMainTable(ctx, c, "catalog_assets"); err != nil {
				return err
			}
			return rbFilterMediaIntegerRowid(ctx, c, "catalog_assets")
		}, []string{"SEARCH a USING INTEGER PRIMARY KEY (rowid=?)"}},
		{"sources.bytes", rbFilterMediaSourcesOctetsSQL, []stmt.Param{s("library"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"library_sources_library"}, Seeks: []string{"library_id=?"}}, func(ctx context.Context, c *sql.Conn) error {
			return rbFilterMediaIndexAudit(ctx, c, "library_sources", "library_sources_library", "c", []string{"library_id", "id"})
		}, []string{"SEARCH library_sources USING INDEX library_sources_library (library_id=?)"}},
		{"streams.bytes", rbFilterMediaStreamsOctetsSQL, []stmt.Param{s("asset"), i("after"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}}, func(ctx context.Context, c *sql.Conn) error {
			return auditClosedOctetKey(ctx, c, "asset_streams", "sqlite_autoindex_asset_streams_1", []string{"asset_id", "stream_index"})
		}, []string{"SEARCH s USING INDEX sqlite_autoindex_asset_streams_1 (asset_id=? AND stream_index>?)"}},
		{"streams.subtitles.bytes", rbFilterMediaSubtitlesOctetsSQL, []stmt.Param{s("asset"), s("after"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>?"}}, func(ctx context.Context, c *sql.Conn) error {
			return rbFilterMediaIndexAudit(ctx, c, "asset_subtitles", "asset_subtitles_asset", "c", []string{"asset_id", "id"})
		}, []string{"SEARCH t USING INDEX asset_subtitles_asset (asset_id=? AND id>?)"}},
		{"streams.subtitles.resource.bytes", rbFilterMediaSubtitleResourcesOctetsSQL, []stmt.Param{i("item"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"subtitle_resources_item", "sqlite_autoindex_subtitle_revisions_1", "sqlite_autoindex_catalog_assets_1"}, Seeks: []string{"item_id=?", "resource_id=? AND revision=?", "token=?"}}, func(ctx context.Context, c *sql.Conn) error {
			if err := rbFilterMediaOctetFunction(ctx, c); err != nil {
				return err
			}
			if err := rbFilterMediaIndexAudit(ctx, c, "subtitle_resources", "subtitle_resources_item", "c", []string{"item_id", "source_id", "deleted", "scope", "owner"}); err != nil {
				return err
			}
			if err := rbFilterMediaIndexAudit(ctx, c, "subtitle_revisions", "sqlite_autoindex_subtitle_revisions_1", "pk", []string{"resource_id", "revision"}); err != nil {
				return err
			}
			return rbFilterMediaIndexAudit(ctx, c, "catalog_assets", "sqlite_autoindex_catalog_assets_1", "u", []string{"token"})
		}, []string{"SEARCH r USING INDEX subtitle_resources_item (item_id=?)", "SEARCH rev USING INDEX sqlite_autoindex_subtitle_revisions_1 (resource_id=? AND revision=?)", "SEARCH a USING INDEX sqlite_autoindex_catalog_assets_1 (token=?)"}},
		{"publication.bytes", rbFilterMediaPublicationOctetsSQL, []stmt.Param{i("item"), i("after"), i("limit")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"p"}}, func(ctx context.Context, c *sql.Conn) error {
			return auditClosedOctetKey(ctx, c, "reading_publications", "", []string{"item_id", "asset_id"})
		}, []string{"SEARCH p USING PRIMARY KEY (item_id=? AND asset_id>?)"}},
	}
	for _, shape := range shapes {
		shape := shape
		id := stmt.ID("read.browse.rb.filter.media." + shape.id)
		pointOctets.add(octetShape{id: id, sql: shape.sql, mismatch: "exact media filter byte-point definition required",
			definition: func(d stmt.Definition) bool {
				return d.ID == id && d.SQL == shape.sql && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, shape.params) && reflect.DeepEqual(d.Plan, shape.plan)
			},
			audit: shape.audit,
			point: func(eqp Rows) error {
				if len(eqp.Values) != len(shape.rows) {
					return fmt.Errorf("media filter bytes: exact point plan required")
				}
				for n, want := range shape.rows {
					if len(eqp.Values[n]) != 4 || eqp.Values[n][3] != want {
						return fmt.Errorf("media filter bytes: expected %q", want)
					}
				}
				return nil
			},
		})
	}
}

// rbFilterMediaOctetFunction proves the preflight uses the unshadowed native
// one-argument octet_length, never a SQL or TEMP object with that name.
func rbFilterMediaOctetFunction(ctx context.Context, c *sql.Conn) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("media filter bytes: invalid native function metadata")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("media filter bytes: invalid native function identity")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("media filter bytes: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("media filter bytes: exact native octet_length required")
	}
	return nil
}

// rbFilterMediaMainTable proves the object is an ordinary MAIN table.
func rbFilterMediaMainTable(ctx context.Context, c *sql.Conn, table string) error {
	tables, err := query(ctx, c, "PRAGMA main.table_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	if len(tables.Values) != 1 || len(tables.Values[0]) != 6 || tables.Values[0][0] != "main" || tables.Values[0][1] != table || tables.Values[0][2] != "table" {
		return fmt.Errorf("media filter bytes: ordinary MAIN table required")
	}
	return nil
}

// rbFilterMediaIndexAudit proves the native index still has the compiled exact
// key. It also proves the unshadowed builtin octet_length used by the preflight.
func rbFilterMediaIndexAudit(ctx context.Context, c *sql.Conn, table, index, origin string, keys []string) error {
	if err := rbFilterMediaOctetFunction(ctx, c); err != nil {
		return err
	}
	if err := rbFilterMediaMainTable(ctx, c, table); err != nil {
		return err
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("media filter bytes: invalid native index metadata")
		}
		if row[1] != index {
			continue
		}
		if row[3] != origin {
			return fmt.Errorf("media filter bytes: native index origin changed")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("media filter bytes: native index absent")
	}
	info, err := query(ctx, c, "PRAGMA main.index_xinfo("+identifier(index)+")")
	if err != nil {
		return err
	}
	seen := 0
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("media filter bytes: invalid native index column metadata")
		}
		key, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("media filter bytes: invalid native index key metadata")
		}
		if key != 1 {
			continue
		}
		if seen >= len(keys) || row[2] != keys[seen] || row[3] != int64(0) || row[4] != "BINARY" {
			return fmt.Errorf("media filter bytes: native index key changed")
		}
		seen++
	}
	if seen != len(keys) {
		return fmt.Errorf("media filter bytes: native index key incomplete")
	}
	return nil
}

// rbFilterMediaIntegerRowid proves catalog_assets.id is still the INTEGER
// PRIMARY KEY rowid alias the unique point relies on.
func rbFilterMediaIntegerRowid(ctx context.Context, c *sql.Conn, table string) error {
	columns, err := query(ctx, c, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if err != nil {
		return err
	}
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("media filter bytes: invalid native column metadata")
		}
		if row[1] != "id" {
			continue
		}
		if row[2] != "INTEGER" || row[5] != int64(1) || row[6] != int64(0) {
			return fmt.Errorf("media filter bytes: exact integer rowid identity required")
		}
		return nil
	}
	return fmt.Errorf("media filter bytes: integer rowid identity missing")
}
