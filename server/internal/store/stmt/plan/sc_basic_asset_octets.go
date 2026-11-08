package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const scBasicAssetLengthsID stmt.ID = "scan.basic_projection.asset_lengths"
const scBasicAssetLengthsSQL = "SELECT octet_length(token),octet_length(container),octet_length(video_codec),octet_length(audio_codec),octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?"

func init() {
	pointOctets.add(octetShape{scBasicAssetLengthsID, scBasicAssetLengthsSQL,
		"Basic asset byte preflight requires the exact closed point definition", scBasicAssetOctetDefinition,
		auditSCBasicAssetOctetKey,
		func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("Basic asset preflight requires one INTEGER PRIMARY KEY SEARCH")
			}
			return nil
		}})
}

func auditSCBasicAssetOctetKey(ctx context.Context, c *sql.Conn) error {
	if err := auditBuiltinOctetLength(ctx, c); err != nil {
		return err
	}
	columns, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_assets)")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("Basic asset preflight: invalid native columns")
		}
		if row[5] != int64(0) {
			if row[1] != "id" || row[2] != "INTEGER" || row[5] != int64(1) || row[6] != int64(0) {
				return fmt.Errorf("Basic asset preflight: exact INTEGER id primary key required")
			}
			keys++
		}
	}
	if keys != 1 {
		return fmt.Errorf("Basic asset preflight: native primary key missing")
	}
	return nil
}

func scBasicAssetOctetDefinition(d stmt.Definition) bool {
	return d.ID == scBasicAssetLengthsID && d.SQL == scBasicAssetLengthsSQL && d.Kind == stmt.Read && d.Plan.Hot &&
		slices.Equal(d.Params, []stmt.Param{{Name: "asset_id", Type: stmt.Integer}, {Name: "library_id", Type: stmt.Integer}}) &&
		slices.Equal(d.Plan.PrimaryKeys, []string{"catalog_assets"}) && slices.Equal(d.Plan.Seeks, []string{"rowid=?"}) &&
		len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}
