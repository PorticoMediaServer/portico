package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const basicResumeLengthsID stmt.ID = "scan.basic_stage.resume_lengths"
const basicResumeLengthsSQL = "SELECT octet_length(copy_key),octet_length(copy_key2),octet_length(copy_key3),octet_length(fault) FROM catalog_info_changes WHERE id=? AND job_revision=?"

func init() {
	pointOctets.add(octetShape{basicResumeLengthsID, basicResumeLengthsSQL,
		"Basic resume byte preflight requires the exact closed point definition", basicResumeOctetDefinition,
		auditBasicResumeOctetKey,
		func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH catalog_info_changes USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("Basic resume preflight requires one INTEGER PRIMARY KEY SEARCH")
			}
			return nil
		}})
}

func auditBasicResumeOctetKey(ctx context.Context, c *sql.Conn) error {
	if err := auditBuiltinOctetLength(ctx, c); err != nil {
		return err
	}
	columns, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_info_changes)")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("Basic resume preflight: invalid native columns")
		}
		if row[5] != int64(0) {
			if row[1] != "id" || row[2] != "INTEGER" || row[5] != int64(1) || row[6] != int64(0) {
				return fmt.Errorf("Basic resume preflight: exact INTEGER id primary key required")
			}
			keys++
		}
	}
	if keys != 1 {
		return fmt.Errorf("Basic resume preflight: native primary key missing")
	}
	return nil
}

func basicResumeOctetDefinition(d stmt.Definition) bool {
	return d.ID == basicResumeLengthsID && d.SQL == basicResumeLengthsSQL && d.Kind == stmt.Read && d.Plan.Hot &&
		slices.Equal(d.Params, []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "revision", Type: stmt.Integer}}) &&
		slices.Equal(d.Plan.PrimaryKeys, []string{"catalog_info_changes"}) && slices.Equal(d.Plan.Seeks, []string{"rowid=?"}) &&
		len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}
