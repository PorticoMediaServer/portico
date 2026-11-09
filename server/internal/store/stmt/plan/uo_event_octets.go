package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// UO event byte preflights. The SQL is repeated byte for byte from the read
// package on purpose: this registry is the authority, so drift between a read
// statement and its preflight refuses Capture instead of silently pairing a
// different projection. Only the native one-argument octet_length call is
// admitted by the closed grammar, and every point is a keyed SEARCH on the
// api_events PK, never a SCAN or a temporary B-tree.
const (
	uoEventOctetsID         stmt.ID = "read.uo.events.octets"
	uoEventAudienceOctetsID stmt.ID = "read.uo.events.audience_octets"
)

const uoEventOctetsSQL = `SELECT octet_length(type),octet_length(resource_kind),octet_length(resource_id),octet_length(revision),octet_length(data) FROM api_events WHERE id=? AND audience=?`

const uoEventAudienceOctetsSQL = `SELECT octet_length(audience) FROM api_events WHERE id=?`

func init() {
	pointOctets.add(octetShape{
		id: uoEventOctetsID, sql: uoEventOctetsSQL,
		mismatch:   "UO event bytes require the exact closed api_events point",
		definition: uoEventOctetDefinition(uoEventOctetsID, uoEventOctetsSQL, "store/read UO events", []stmt.Param{{Name: "id", Type: stmt.Integer}, {Name: "audience", Type: stmt.Text}}),
		audit:      auditUOEventOctets,
		point:      uoEventOctetPoint,
	})
	pointOctets.add(octetShape{
		id: uoEventAudienceOctetsID, sql: uoEventAudienceOctetsSQL,
		mismatch:   "UO event audience bytes require the exact closed api_events point",
		definition: uoEventOctetDefinition(uoEventAudienceOctetsID, uoEventAudienceOctetsSQL, "store/read UO event feed", []stmt.Param{{Name: "id", Type: stmt.Integer}}),
		audit:      auditUOEventOctets,
		point:      uoEventOctetPoint,
	})
}

func uoEventOctetDefinition(id stmt.ID, sql, owner string, params []stmt.Param) func(stmt.Definition) bool {
	want := stmt.Definition{
		ID: id, Kind: stmt.Read, Owner: owner, Params: params,
		Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"api_events"}},
	}
	return func(d stmt.Definition) bool {
		return d.ID == want.ID && d.SQL == sql && d.Kind == want.Kind &&
			d.Owner == want.Owner && reflect.DeepEqual(d.Params, want.Params) && reflect.DeepEqual(d.Plan, want.Plan)
	}
}

func auditUOEventOctets(ctx context.Context, conn *sql.Conn) error {
	if err := auditBuiltinOctetLength(ctx, conn); err != nil {
		return err
	}
	return mainBaseTable(ctx, conn, "api_events")
}

// uoEventOctetPoint requires the exact one-row INTEGER PRIMARY KEY point: the
// event id is the rowid, so no other access path may serve the preflight.
func uoEventOctetPoint(rows Rows) error {
	if len(rows.Values) != 1 || len(rows.Values[0]) != 4 {
		return fmt.Errorf("UO event bytes: exact one-row point plan required")
	}
	detail, ok := rows.Values[0][3].(string)
	if !ok {
		return fmt.Errorf("UO event bytes: invalid EQP")
	}
	if strings.HasPrefix(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
		return fmt.Errorf("UO event bytes: indexed SEARCH required")
	}
	if !strings.Contains(detail, "USING INTEGER PRIMARY KEY (rowid=?)") {
		return fmt.Errorf("UO event bytes: exact api_events rowid point required")
	}
	return nil
}
