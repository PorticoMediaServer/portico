package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact MP points join the shared per-domain registry. No exported registrar or
// generic function allowlist is added. These lengths alone confer no authority.
func init() {
	for _, p := range []struct{ id, table, columns string }{
		{"queue", "queues_v1", "id,current_entry_id,session_id,repeat,owner_kind,owner_id,authority,account_id,profile_id"},
		{"session", "playback_v1_sessions", "id,kind,role,state,version_id,queue_id,entry_id,media_session_id,end_reason,device_id,account_id,profile_id,authority,group_id,transfer_id,channel_id"},
		{"prepared", "prepared_media_jobs", "id,state,phase,error_code,version_id,asset_id,target_id,profile_id,source_revision,library_id"},
	} {
		table := p.table
		index := "sqlite_autoindex_" + table + "_1"
		columns := strings.Split(p.columns, ",")
		for n, c := range columns {
			columns[n] = "octet_length(" + c + ")"
		}
		querySQL := "SELECT " + strings.Join(columns, ",") + " FROM " + table + " INDEXED BY " + index + " WHERE id=?"
		pointOctets.add(octetShape{stmt.ID("read.foreground.mp." + p.id + "_octets"), querySQL, "MP byte preflight requires the exact compiled ID point",
			func(d stmt.Definition) bool { return mpOctetDefinition(d, table, index) },
			func(ctx context.Context, c *sql.Conn) error { return auditMPOctetKey(ctx, c, table, index) },
			func(eqp Rows) error {
				want := "SEARCH " + table + " USING INDEX " + index + " (id=?)"
				if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
					return fmt.Errorf("MP preflight: exact primary-key point SEARCH required")
				}
				return nil
			},
		})
	}
}
func mpOctetDefinition(d stmt.Definition, table, index string) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 1 || d.Params[0].Type != stmt.Text || d.Params[0].Nullable || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != index || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != "id=?" {
		return false
	}
	pin := d.Plan.AutoIndexes[0]
	return pin.Table == table && pin.Name == index && pin.Origin == "pk" && len(pin.Columns) == 1 && pin.Columns[0] == (stmt.IndexColumn{Name: "id", Collation: "BINARY"})
}
func auditMPOctetKey(ctx context.Context, conn *sql.Conn, table, index string) error {
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, r := range functions.Values {
		if len(r) != 6 {
			return fmt.Errorf("MP preflight: native function metadata")
		}
		name, ok := r[0].(string)
		arity, integer := r[4].(int64)
		if !ok || !integer {
			return fmt.Errorf("MP preflight: native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if r[1] != int64(1) || arity != 1 {
				return fmt.Errorf("MP preflight: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("MP preflight: unshadowed native octet_length required")
	}
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+table+")")
	if err != nil {
		return err
	}
	found := false
	for _, r := range indexes.Values {
		if len(r) != 5 {
			return fmt.Errorf("MP preflight: native index metadata")
		}
		if r[1] == index {
			if r[2] != int64(1) || r[3] != "pk" || r[4] != int64(0) {
				return fmt.Errorf("MP preflight: nonpartial primary key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MP preflight: primary key missing")
	}
	info, err := query(ctx, conn, "PRAGMA main.index_info("+index+")")
	if err != nil {
		return err
	}
	if len(info.Values) != 1 || len(info.Values[0]) != 3 || info.Values[0][2] != "id" {
		return fmt.Errorf("MP preflight: native key must be exactly id")
	}
	return nil
}
