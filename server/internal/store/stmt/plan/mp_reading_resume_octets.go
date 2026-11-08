package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MP reading resume display owns its closed child byte point here. The SQL bytes
// are deliberately repeated: plan's registry is the authority, and a drift
// between the read statement and this preflight fails Capture instead of
// silently pairing a different projection.
//
// personal_reading_resumes is the exact group F reading child from internal
// planning/v3/schema-v1/phase2/changes/F-media-restart/F-READING.sql. Group F is
// not embedded yet, so this point is registered and linted now but its native
// Capture audit can only run once the child table is part of the embedded
// layout; the read tests prove the same preflight on the exact draft DDL.
const (
	mpReadingResumeChildOctetsID stmt.ID = "read.browse.mp.reading.resume.child_octets"
	mpReadingResumeLatestIndex           = "personal_reading_resumes_latest"
	mpReadingResumeChildIndex            = "sqlite_autoindex_personal_reading_resumes_1"
)

const mpReadingResumeChildOctetsSQL = `SELECT octet_length(r.source_token),octet_length(r.source_revision),octet_length(r.format),octet_length(r.cfi),octet_length(r.section) FROM personal_reading_resumes AS r INDEXED BY sqlite_autoindex_personal_reading_resumes_1 WHERE r.personal_item_id=? AND r.asset_id=?`

var mpReadingResumeChildAuto = stmt.AutoIndexBinding{Table: "personal_reading_resumes", Name: mpReadingResumeChildIndex, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "personal_item_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}

func init() {
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	params := []stmt.Param{integer("personal"), integer("asset")}
	proof := stmt.Plan{Hot: true, Indexes: []string{mpReadingResumeChildIndex}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingResumeChildAuto}, Seeks: []string{"personal_item_id=? AND asset_id=?"}}
	pointOctets.add(octetShape{mpReadingResumeChildOctetsID, mpReadingResumeChildOctetsSQL,
		"MP reading resume bytes require the exact selected child point",
		func(d stmt.Definition) bool {
			return d.ID == mpReadingResumeChildOctetsID && d.SQL == mpReadingResumeChildOctetsSQL && d.Kind == stmt.Read && d.Owner == "store/read MP" && len(d.CallSites) == 1 && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof) && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0
		},
		auditMPReadingResumeChildOctets, checkMPReadingResumeChildOctets})
}

func auditMPReadingResumeChildOctets(ctx context.Context, c *sql.Conn) error {
	if err := auditClosedOctetKey(ctx, c, "personal_reading_resumes", mpReadingResumeChildIndex, []string{"personal_item_id", "asset_id"}); err != nil {
		return err
	}
	// The latest-version choice is hot and must stay one ordered SEARCH: the
	// complete native key (personal_item_id, observed_ms DESC, asset_id DESC).
	indexes, err := query(ctx, c, "PRAGMA main.index_list(personal_reading_resumes)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("MP reading resume preflight: invalid child index metadata")
		}
		if row[1] == mpReadingResumeLatestIndex {
			if row[2] != int64(0) || row[3] != "c" || row[4] != int64(0) {
				return fmt.Errorf("MP reading resume preflight: plain nonunique latest index required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MP reading resume preflight: latest-version index missing")
	}
	info, err := query(ctx, c, "PRAGMA main.index_xinfo("+mpReadingResumeLatestIndex+")")
	if err != nil {
		return err
	}
	names := []string{"personal_item_id", "observed_ms", "asset_id"}
	descending := []int64{0, 1, 1}
	n := 0
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("MP reading resume preflight: invalid latest index metadata")
		}
		if row[5] != int64(1) {
			continue
		}
		if n >= len(names) || row[2] != names[n] || row[3] != descending[n] || row[4] != "BINARY" {
			return fmt.Errorf("MP reading resume preflight: latest index key must be exactly personal_item_id,observed_ms DESC,asset_id DESC")
		}
		n++
	}
	if n != len(names) {
		return fmt.Errorf("MP reading resume preflight: latest index key incomplete")
	}
	return nil
}

func checkMPReadingResumeChildOctets(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 {
		return fmt.Errorf("MP reading resume octets: one exact child SEARCH required")
	}
	if eqp.Values[0][3] != "SEARCH r USING INDEX "+mpReadingResumeChildIndex+" (personal_item_id=? AND asset_id=?)" {
		return fmt.Errorf("MP reading resume octets: exact child SEARCH required")
	}
	return nil
}
