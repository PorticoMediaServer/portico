package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const mgChannelOctetsID stmt.ID = "read.mg.guide.channel_octets"
const mgProgrammeOctetsID stmt.ID = "read.mg.guide.programme_octets"
const mgChannelOctetsSQL = "SELECT octet_length(channel_id),octet_length(name),octet_length(number),octet_length(group_name),octet_length(logo_url) FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?"
const mgProgrammeOctetsSQL = "SELECT octet_length(id),octet_length(channel_id),octet_length(title),octet_length(start_utc),octet_length(end_utc),octet_length(metadata_series_id),octet_length(metadata_episode_id),octet_length(metadata_new_evidence),octet_length(metadata_description),octet_length(metadata_facts),octet_length(icon_url) FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id=?"

func init() {
	pointOctets.add(octetShape{mgChannelOctetsID, mgChannelOctetsSQL,
		"MG channel byte preflight requires the exact closed read definition",
		func(d stmt.Definition) bool { return mgOctetDefinition(d, "live_channel_versions", "channel_id") },
		func(ctx context.Context, c *sql.Conn) error {
			return auditMGOctetKey(ctx, c, "live_channel_versions", "channel_id")
		},
		func(eqp Rows) error { return checkMGOctetPoint(eqp, "live_channel_versions", "channel_id") }})
	pointOctets.add(octetShape{mgProgrammeOctetsID, mgProgrammeOctetsSQL,
		"MG programme byte preflight requires the exact closed read definition",
		func(d stmt.Definition) bool { return mgOctetDefinition(d, "live_programmes", "id") },
		func(ctx context.Context, c *sql.Conn) error { return auditMGOctetKey(ctx, c, "live_programmes", "id") },
		func(eqp Rows) error { return checkMGOctetPoint(eqp, "live_programmes", "id") }})
}

func mgOctetDefinition(d stmt.Definition, table, identity string) bool {
	index := "sqlite_autoindex_" + table + "_1"
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != index ||
		len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != "generation_id=? AND "+identity+"=?" ||
		len(d.Plan.AutoIndexes) != 1 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for _, p := range d.Params {
		if p.Type != stmt.Text || p.Nullable {
			return false
		}
	}
	pin := d.Plan.AutoIndexes[0]
	if pin.Table != table || pin.Name != index || pin.Origin != "pk" || len(pin.Columns) != 2 {
		return false
	}
	return pin.Columns[0] == (stmt.IndexColumn{Name: "generation_id", Collation: "BINARY"}) &&
		pin.Columns[1] == (stmt.IndexColumn{Name: identity, Collation: "BINARY"})
}

func auditMGOctetKey(ctx context.Context, conn *sql.Conn, table, identity string) error {
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("MG preflight: unexpected native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("MG preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("MG preflight: unshadowed native one-argument octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("MG preflight: unshadowed native one-argument octet_length required")
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
			return fmt.Errorf("MG preflight: unexpected native index metadata")
		}
		if row[1] == index {
			if row[2] != int64(1) || row[3] != "pk" || row[4] != int64(0) {
				return fmt.Errorf("MG preflight: exact nonpartial primary key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MG preflight: pinned primary key missing")
	}
	info, err := query(ctx, conn, "PRAGMA main.index_info("+index+")")
	if err != nil {
		return err
	}
	if len(info.Values) != 2 || len(info.Values[0]) != 3 || len(info.Values[1]) != 3 || info.Values[0][2] != "generation_id" || info.Values[1][2] != identity {
		return fmt.Errorf("MG preflight: exact generation/identity primary key required")
	}
	return nil
}

func checkMGOctetPoint(eqp Rows, table, identity string) error {
	want := "SEARCH " + table + " USING INDEX sqlite_autoindex_" + table + "_1 (generation_id=? AND " + identity + "=?)"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("MG preflight: exact indexed generation/identity point SEARCH required")
	}
	return nil
}
