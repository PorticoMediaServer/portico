package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"slices"
)

// Only exact complete native PK points. The bounded pending-next identity
// range uses no byte-function allowance and carries no payload authority.
func init() {
	for _, p := range []struct {
		id              stmt.ID
		sql, table, key string
		params          []stmt.Param
	}{
		{"mp.repeat.next_bytes", `SELECT octet_length(token),octet_length(media_session_id) FROM playback_v1_prepared INDEXED BY sqlite_autoindex_playback_v1_prepared_1 WHERE token=? AND queue_id=? AND state='prepared'`, "playback_v1_prepared", "token", []stmt.Param{{Name: "next", Type: stmt.Text}, {Name: "queue", Type: stmt.Text}}},
		{"mp.repeat.stop_bytes", `SELECT octet_length(mp_retire_request_id),mp_retired_generation,mp_exited_generation,mp_revision,octet_length(mp_execution_id) FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=?`, "playback_sessions", "id", []stmt.Param{{Name: "media", Type: stmt.Text}, {Name: "generation", Type: stmt.Integer}}},
	} {
		p := p
		index := "sqlite_autoindex_" + p.table + "_1"
		pointOctets.add(octetShape{id: p.id, sql: p.sql, mismatch: "MP worker octets require exact compiled PK definition", definition: func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && d.SQL == p.sql && slices.Equal(d.Params, p.params) && d.Plan.Hot && slices.Equal(d.Plan.Indexes, []string{index}) && len(d.Plan.PrimaryKeys) == 0 && len(d.Plan.Seeks) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == "" && d.Plan.NativeRead == nil && slices.EqualFunc(d.Plan.AutoIndexes, []stmt.AutoIndexBinding{{Table: p.table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: p.key, Collation: "BINARY"}}}}, func(a, b stmt.AutoIndexBinding) bool {
				return a.Table == b.Table && a.Name == b.Name && a.Origin == b.Origin && slices.Equal(a.Columns, b.Columns)
			})
		}, audit: func(ctx context.Context, c *sql.Conn) error {
			return auditClosedOctetKey(ctx, c, p.table, index, []string{p.key})
		}, point: func(rows Rows) error {
			want := "SEARCH " + p.table + " USING INDEX " + index + " (" + p.key + "=?)"
			if len(rows.Values) != 1 || len(rows.Values[0]) != 4 || rows.Values[0][3] != want {
				return fmt.Errorf("MP worker requires exact native point SEARCH %s", want)
			}
			return nil
		}})
	}
}
