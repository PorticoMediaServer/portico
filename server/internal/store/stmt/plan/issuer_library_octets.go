package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const issuerLibraryRootLengthsID stmt.ID = "scan.issuer.library_root_lengths"
const issuerLibraryRootLengthsSQL = "SELECT octet_length(root) FROM catalog_libraries WHERE id=? AND library_id=?"

// This second closed issuer point does not admit octet_length to generic lint.
// Keep the existing source-length definition and its native audit unchanged.
func closedIssuerOctetDefinition(d stmt.Definition) bool {
	return issuerOctetDefinition(d) || issuerLibraryOctetDefinition(d) || n1OctetDefinition(d) || wcProgressSnapshotOctetDefinition(d) || directoryOctetDefinition(d) || directoryTerminalOctetDefinition(d)
}

func issuerLibraryOctetDefinition(d stmt.Definition) bool {
	return d.ID == issuerLibraryRootLengthsID && d.SQL == issuerLibraryRootLengthsSQL &&
		d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 &&
		d.Params[0].Name == "library_id" && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable &&
		d.Params[1].Name == "library_key" && d.Params[1].Type == stmt.Text && !d.Params[1].Nullable &&
		len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "catalog_libraries" &&
		len(d.Plan.Seeks) == 1 && d.Plan.Seeks[0] == "rowid=?" &&
		len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 &&
		len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 &&
		d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}

func auditIssuerLibraryOctetKey(ctx context.Context, conn *sql.Conn) error {
	if err := auditBuiltinOctetLength(ctx, conn); err != nil {
		return err
	}
	// Certify the INTEGER PRIMARY KEY alias, rather than trusting a schema name.
	columns, err := query(ctx, conn, "PRAGMA main.table_xinfo(catalog_libraries)")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("issuer library preflight: unexpected native column metadata")
		}
		if row[5] != int64(0) {
			if row[1] != "id" || row[2] != "INTEGER" || row[5] != int64(1) || row[6] != int64(0) {
				return fmt.Errorf("issuer library preflight: native key must be exactly INTEGER id")
			}
			keys++
		}
	}
	if keys != 1 {
		return fmt.Errorf("issuer library preflight: native INTEGER id key missing")
	}
	return nil
}

func checkIssuerLibraryOctetPoint(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH catalog_libraries USING INTEGER PRIMARY KEY (rowid=?)" {
		return fmt.Errorf("issuer library preflight: exact INTEGER PRIMARY KEY point SEARCH required")
	}
	return nil
}
