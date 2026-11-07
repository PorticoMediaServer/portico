package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const issuerSourceLengthsID stmt.ID = "scan.issuer.source_lengths"
const issuerSourceLengthsSQL = "SELECT octet_length(id),octet_length(library_id),octet_length(kind),octet_length(root),octet_length(incarnation),octet_length(root_identity),octet_length(health),octet_length(classification) FROM library_sources WHERE id=? AND library_id=?"
const issuerSourceIndex = "sqlite_autoindex_library_sources_1"

func init() {
	pointOctets.add(octetShape{issuerSourceLengthsID, issuerSourceLengthsSQL,
		"issuer byte preflight requires the exact closed read definition",
		issuerOctetDefinition, auditIssuerOctetKey, checkIssuerOctetPoint})
}

// This is one closed issuer preflight, not a function allowlist. Exact SQL bytes
// fix every argument to a named column and fix the two point predicates. Public
// connection-free lint still refuses octet_length; only Capture can certify it.
func issuerOctetDefinition(d stmt.Definition) bool {
	if d.ID != issuerSourceLengthsID || d.SQL != issuerSourceLengthsSQL || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Params) != 2 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != issuerSourceIndex {
		return false
	}
	for _, p := range d.Params {
		if p.Type != stmt.Text || p.Nullable {
			return false
		}
	}
	return true
}

// Source classification is checked by the ordinary MAIN/TEMP/view guard. Check
// the live native key too: the single id equality must bound at most one row.
func auditIssuerOctetKey(ctx context.Context, conn *sql.Conn) error {
	// Read the native pragma directly. A TEMP or MAIN object named
	// pragma_function_list must never be able to forge this builtin audit.
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("issuer preflight: unexpected native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("issuer preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("issuer preflight: unshadowed native one-argument octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("issuer preflight: unshadowed native one-argument octet_length required")
	}
	indexes, err := query(ctx, conn, "PRAGMA main.index_list(library_sources)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("issuer preflight: unexpected native index metadata")
		}
		if row[1] == issuerSourceIndex {
			if _, err := autoindexOrigin(row); err != nil {
				return fmt.Errorf("issuer preflight: %w", err)
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("issuer preflight: pinned native id key missing")
	}
	info, err := query(ctx, conn, "PRAGMA main.index_info("+issuerSourceIndex+")")
	if err != nil {
		return err
	}
	if len(info.Values) != 1 || len(info.Values[0]) != 3 || info.Values[0][2] != "id" {
		return fmt.Errorf("issuer preflight: native key must be exactly id")
	}
	return nil
}

func checkIssuerOctetPoint(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH library_sources USING INDEX "+issuerSourceIndex+" (id=?)" {
		return fmt.Errorf("issuer preflight: exact indexed id point SEARCH required")
	}
	return nil
}
