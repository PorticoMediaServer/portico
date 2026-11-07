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
const mgLCCandidateOctetsSQL = "SELECT duration_ms,octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title) FROM lc_candidates INDEXED BY sqlite_autoindex_lc_candidates_1 WHERE generation_id=? AND rule_id=? AND item_id=?"

// 113 is the fixed MG entry accounting recipe, admitted only by these exact SQL bytes.
const mgLCEntryOctetsSQL = "SELECT 113+octet_length(generation_id)+octet_length(occurrence_id)+octet_length(channel_id)+octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title)+octet_length(rule_id)+octet_length(block_id)+octet_length(slate_reason) FROM lc_entries INDEXED BY sqlite_autoindex_lc_entries_1 WHERE generation_id=? AND occurrence_id=?"

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
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 1+len(identities) || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != index ||
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
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("MG LC preflight: unexpected native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("MG LC preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("MG LC preflight: unshadowed native one-argument octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("MG LC preflight: unshadowed native one-argument octet_length required")
	}
	// Table/index strings come only from the two compiled registrations above.
	index := "sqlite_autoindex_" + table + "_1"
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+table+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("MG LC preflight: unexpected native index metadata")
		}
		if row[1] == index {
			if row[2] != int64(1) || row[3] != "pk" || row[4] != int64(0) {
				return fmt.Errorf("MG LC preflight: exact nonpartial primary key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MG LC preflight: pinned primary key missing")
	}
	columns := append([]string{"generation_id"}, identities...)
	info, err := query(ctx, conn, "PRAGMA main.index_xinfo("+index+")")
	if err != nil {
		return err
	}
	if len(info.Values) != len(columns)+1 {
		return fmt.Errorf("MG LC preflight: exact full primary key required")
	}
	for i, name := range columns {
		row := info.Values[i]
		if len(row) != 6 || row[0] != int64(i) || row[2] != name || row[3] != int64(0) || row[4] != "BINARY" || row[5] != int64(1) {
			return fmt.Errorf("MG LC preflight: exact BINARY primary key required")
		}
		if cid, ok := row[1].(int64); !ok || cid < 0 {
			return fmt.Errorf("MG LC preflight: native column required")
		}
	}
	tail := info.Values[len(columns)]
	if len(tail) != 6 || tail[1] != int64(-1) || tail[5] != int64(0) {
		return fmt.Errorf("MG LC preflight: native rowid tail required")
	}

	return nil
}

func checkMGLCOctetPoint(eqp Rows, table string, identities ...string) error {
	want := "SEARCH " + table + " USING INDEX sqlite_autoindex_" + table + "_1 (generation_id=? AND " + strings.Join(identities, "=? AND ") + "=?)"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("MG LC preflight: exact indexed generation/identity point SEARCH required")
	}
	return nil
}
