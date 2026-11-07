package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"slices"
	"strings"
)

const wcProgressSnapshotOctetsID stmt.ID = "wc.progress.snapshot_octets"
const wcProgressSnapshotOctetsSQL = "SELECT octet_length(s.state) FROM personal_items AS i INDEXED BY personal_current_live INNER JOIN personal_snapshots AS s ON s.profile_id=i.profile_id AND s.item_id=i.item_id AND s.revision=i.revision WHERE i.profile_id=? AND i.item_id=?"

func init() {
	pointOctets.add(octetShape{
		id: wcProgressSnapshotOctetsID, sql: wcProgressSnapshotOctetsSQL,
		mismatch:   "complete closed WC snapshot length point required",
		definition: wcProgressSnapshotOctetDefinition,
		audit:      auditWCProgressSnapshotOctets,
		// Preserve WC's existing EQP enforcement in the common checkSearch.
		point: func(Rows) error { return nil },
	})
}

// Exactly one additional native length point. Generic SQL lint still refuses
// function calls. Complete definition equality prevents a different projection,
// range, aggregate or function from borrowing this permission.
func wcProgressSnapshotOctetDefinition(d stmt.Definition) bool {
	return d.ID == wcProgressSnapshotOctetsID && d.SQL == wcProgressSnapshotOctetsSQL && d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 && d.Params[0] == (stmt.Param{Name: "profile", Type: stmt.Text}) && d.Params[1] == (stmt.Param{Name: "subject", Type: stmt.Integer}) && slices.Equal(d.Plan.Indexes, []string{"personal_current_live", "sqlite_autoindex_personal_snapshots_1"}) && len(d.Plan.PrimaryKeys) == 0 && len(d.Plan.Seeks) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && len(d.Plan.AutoIndexes) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}
func auditBuiltinOctetLength(ctx context.Context, conn *sql.Conn) error {
	// A direct native PRAGMA cannot resolve to a forged TEMP/main catalogue.
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("issuer library preflight: unexpected native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("issuer library preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("issuer library preflight: unshadowed native one-argument octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("issuer library preflight: unshadowed native one-argument octet_length required")
	}
	return nil
}
func auditWCProgressSnapshotOctets(ctx context.Context, conn *sql.Conn) error {
	if err := auditBuiltinOctetLength(ctx, conn); err != nil {
		return err
	}
	for _, point := range []struct {
		table, index string
		columns      []string
	}{{"personal_items", "personal_current_live", []string{"profile_id", "item_id"}}, {"personal_snapshots", "sqlite_autoindex_personal_snapshots_1", []string{"profile_id", "item_id", "revision"}}} {
		indexes, err := query(ctx, conn, "PRAGMA main.index_list("+point.table+")")
		if err != nil {
			return err
		}
		found := false
		for _, row := range indexes.Values {
			if len(row) == 5 && row[1] == point.index {
				if row[2] != int64(1) || row[4] != int64(0) {
					return fmt.Errorf("progress snapshot: full unique point required")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("progress snapshot: native key missing")
		}
		columns, err := query(ctx, conn, "PRAGMA main.index_info("+point.index+")")
		if err != nil {
			return err
		}
		if len(columns.Values) != len(point.columns) {
			return fmt.Errorf("progress snapshot: incomplete key")
		}
		for i, row := range columns.Values {
			if len(row) != 3 || row[0] != int64(i) || row[2] != point.columns[i] {
				return fmt.Errorf("progress snapshot: native key mismatch")
			}
		}
	}
	return nil
}
