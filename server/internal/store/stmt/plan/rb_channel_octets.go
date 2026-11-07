package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rbChannelOctetsID stmt.ID = "read.browse.rb.channel.octets"
const rbChannelOctetsSQL = "SELECT octet_length(c.config_json),octet_length(g.config_json) FROM lc_channels AS c INDEXED BY sqlite_autoindex_lc_channels_1 CROSS JOIN lc_generations AS g INDEXED BY sqlite_autoindex_lc_generations_1 ON g.channel_id=c.id WHERE c.id=? AND g.id=?"

func init() {
	pointOctets.add(octetShape{rbChannelOctetsID, rbChannelOctetsSQL, "RB channel bytes require the exact closed two-point definition", rbChannelOctetDefinition, auditRBChannelOctets, checkRBChannelOctets})
}
func rbChannelOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || !slices.Equal(d.Params, []stmt.Param{{Name: "channel", Type: stmt.Text}, {Name: "generation", Type: stmt.Text}}) || !slices.Equal(d.Plan.Indexes, []string{"sqlite_autoindex_lc_channels_1", "sqlite_autoindex_lc_generations_1"}) || !slices.Equal(d.Plan.Seeks, []string{"id=?"}) || len(d.Plan.AutoIndexes) != 2 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for i, table := range []string{"lc_channels", "lc_generations"} {
		p := d.Plan.AutoIndexes[i]
		if p.Table != table || p.Name != "sqlite_autoindex_"+table+"_1" || p.Origin != "pk" || !slices.Equal(p.Columns, []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}) {
			return false
		}
	}
	return true
}
func checkRBChannelOctets(rows Rows) error {
	wants := []string{"SEARCH c USING INDEX sqlite_autoindex_lc_channels_1 (id=?)", "SEARCH g USING INDEX sqlite_autoindex_lc_generations_1 (id=?)"}
	if len(rows.Values) != 2 {
		return fmt.Errorf("RB channel preflight needs exactly two point SEARCHes")
	}
	for i, row := range rows.Values {
		if len(row) != 4 || row[3] != wants[i] {
			return fmt.Errorf("RB channel preflight point changed")
		}
	}
	return nil
}
func auditRBChannelOctets(ctx context.Context, c *sql.Conn) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	found := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("RB channel function metadata changed")
		}
		name, ok := row[0].(string)
		if !ok {
			return fmt.Errorf("RB channel function name changed")
		}
		if strings.EqualFold(name, "octet_length") && (row[4] == int64(1) || row[4] == int64(-1)) {
			if row[1] != int64(1) || row[4] != int64(1) {
				return fmt.Errorf("RB channel native octet_length is shadowed")
			}
			found++
		}
	}
	if found != 1 {
		return fmt.Errorf("RB channel native octet_length required")
	}
	for _, table := range []string{"lc_channels", "lc_generations"} {
		index := "sqlite_autoindex_" + table + "_1"
		list, e := query(ctx, c, "PRAGMA main.index_list("+table+")")
		if e != nil {
			return e
		}
		found := false
		for _, row := range list.Values {
			if len(row) == 5 && row[1] == index && row[2] == int64(1) && row[3] == "pk" && row[4] == int64(0) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("RB channel exact unique nonpartial primary key required")
		}
		info, e := query(ctx, c, "PRAGMA main.index_xinfo("+index+")")
		if e != nil {
			return e
		}
		keys := 0
		for _, row := range info.Values {
			if len(row) != 6 {
				return fmt.Errorf("RB channel key metadata changed")
			}
			if row[5] == int64(1) {
				keys++
				if row[2] != "id" || row[3] != int64(0) || row[4] != "BINARY" {
					return fmt.Errorf("RB channel exact BINARY id key required")
				}
			}
		}
		if keys != 1 {
			return fmt.Errorf("RB channel primary key must have one key column")
		}
	}
	return nil
}
