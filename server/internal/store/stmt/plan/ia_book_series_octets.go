package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These exact points are the read-side byte preflight for the accepted local
// book-series projection. Group C is not embedded yet, so these shapes carry
// EXPLAIN-only evidence; they add no registration API and each owns its native
// function/key/EQP audit.
const (
	iaBookSeriesEntityOctetsSQL = `SELECT octet_length(sort_key),octet_length(public_id),octet_length(title),octet_length(metadata_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`
	iaBookSeriesHeadOctetsSQL   = `SELECT octet_length(source_revision),octet_length(member_digest) FROM catalog_book_series_heads WHERE book_id=? AND library_id=?`
	iaBookSeriesMemberOctetsSQL = `SELECT octet_length(series_name),octet_length(series_position) FROM catalog_book_series_members WHERE book_id=? AND ordinal=?`
)

func init() {
	type shape struct {
		name, sql, table, eqp, mode, seek string
		index                             string
		keys                              []string
		params                            []stmt.Param
	}
	for _, v := range []shape{
		{"entity", iaBookSeriesEntityOctetsSQL, "catalog_entities", "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)", "rowid", "rowid=?", "", []string{"id"}, []stmt.Param{{Name: "entity", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}},
		{"head", iaBookSeriesHeadOctetsSQL, "catalog_book_series_heads", "SEARCH catalog_book_series_heads USING INTEGER PRIMARY KEY (rowid=?)", "rowid", "rowid=?", "", []string{"book_id"}, []stmt.Param{{Name: "book", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}},
		{"member", iaBookSeriesMemberOctetsSQL, "catalog_book_series_members", "SEARCH catalog_book_series_members USING PRIMARY KEY (book_id=? AND ordinal=?)", "without-rowid", "book_id=? AND ordinal=?", "", []string{"book_id", "ordinal"}, []stmt.Param{{Name: "book", Type: stmt.Integer}, {Name: "ordinal", Type: stmt.Integer}}},
	} {
		v := v
		id := stmt.ID("read.foreground.ia.book_series." + v.name + "_octets")
		pointOctets.add(octetShape{id, v.sql, "IA book series octets require the exact closed point definition", func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != v.seek || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || len(d.Params) != len(v.params) {
				return false
			}
			if v.mode == "index" {
				if len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != v.index || len(d.Plan.PrimaryKeys) != 0 {
					return false
				}
			} else if len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != v.table || len(d.Plan.Indexes) != 0 {
				return false
			}
			for i, p := range d.Params {
				if p.Name != v.params[i].Name || p.Type != v.params[i].Type || p.Nullable {
					return false
				}
			}
			return true
		}, func(ctx context.Context, c *sql.Conn) error {
			if v.mode == "rowid" {
				return auditIARowidOctetKey(ctx, c, v.table, v.keys[0])
			}
			return auditClosedOctetKey(ctx, c, v.table, v.index, v.keys)
		}, func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != v.eqp {
				return fmt.Errorf("IA book series octets require exact point SEARCH: %s", v.eqp)
			}
			return nil
		}})
	}
}
