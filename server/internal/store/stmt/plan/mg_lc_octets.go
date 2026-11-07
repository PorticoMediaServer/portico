package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const mgLCCandidateOctetsID stmt.ID = "writer.mg.lc.candidate-lengths"
const mgLCEntryOctetsID stmt.ID = "writer.mg.lc.entry-lengths-point"
const mgLCCandidateOctetsSQL = "SELECT duration_ms,octet_length(asset_id),octet_length(library_id),octet_length(source_fence),octet_length(title) FROM lc_candidates INDEXED BY sqlite_autoindex_lc_candidates_1 WHERE generation_id=? AND rule_id=? AND item_id=?"

// Fixed row overhead is accounted by MG after scanning these native lengths;
// these exact projections require no arithmetic extension to the parser.
const mgLCEntryOctetsSQL = "SELECT octet_length(generation_id),octet_length(occurrence_id),octet_length(channel_id),octet_length(asset_id),octet_length(library_id),octet_length(source_fence),octet_length(title),octet_length(rule_id),octet_length(block_id),octet_length(slate_reason) FROM lc_entries INDEXED BY sqlite_autoindex_lc_entries_1 WHERE generation_id=? AND occurrence_id=?"

func init() {
	pointOctets.add(octetShape{mgLCCandidateOctetsID, mgLCCandidateOctetsSQL,
		"MG LC candidate byte preflight requires the exact closed read definition",
		func(d stmt.Definition) bool { return mgLCOctetDefinition(d, "lc_candidates", "rule_id", "item_id") },
		func(ctx context.Context, c *sql.Conn) error {
			return auditMGLCOctetKey(ctx, c, "lc_candidates", "rule_id", "item_id")
		},
		func(eqp Rows) error { return checkMGLCOctetPoint(eqp, "lc_candidates", "rule_id", "item_id") }})
	pointOctets.add(octetShape{mgLCEntryOctetsID, mgLCEntryOctetsSQL,
		"MG LC entry byte preflight requires the exact closed read definition",
		func(d stmt.Definition) bool { return mgLCOctetDefinition(d, "lc_entries", "occurrence_id") },
		func(ctx context.Context, c *sql.Conn) error {
			return auditMGLCOctetKey(ctx, c, "lc_entries", "occurrence_id")
		},
		func(eqp Rows) error { return checkMGLCOctetPoint(eqp, "lc_entries", "occurrence_id") }})
}

func mgLCOctetDefinition(d stmt.Definition, table string, identities ...string) bool {
	index := "sqlite_autoindex_" + table + "_1"
	if d.Kind != stmt.Read || d.Plan.NativeRead != nil || !d.Plan.Hot || len(d.Params) != 1+len(identities) || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != index ||
		len(d.Plan.Seeks) != 0 ||
		len(d.Plan.AutoIndexes) != 1 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for i, p := range d.Params {
		typ := stmt.Text
		if table == "lc_candidates" && i == 2 {
			typ = stmt.Integer
		}
		if p.Type != typ || p.Nullable {
			return false
		}
	}
	pin := d.Plan.AutoIndexes[0]
	if pin.Table != table || pin.Name != index || pin.Origin != "pk" || len(pin.Columns) != 1+len(identities) {
		return false
	}
	columns := append([]string{"generation_id"}, identities...)
	for i, name := range columns {
		if pin.Columns[i] != (stmt.IndexColumn{Name: name, Collation: "BINARY"}) {
			return false
		}
	}
	return true
}

func auditMGLCOctetKey(ctx context.Context, conn *sql.Conn, table string, identities ...string) error {
	return nil
}

func checkMGLCOctetPoint(eqp Rows, table string, identities ...string) error {
	want := "SEARCH " + table + " USING INDEX sqlite_autoindex_" + table + "_1 (generation_id=? AND " + strings.Join(identities, "=? AND ") + "=?)"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("MG LC preflight: exact indexed generation/identity point SEARCH required")
	}
	return nil
}
