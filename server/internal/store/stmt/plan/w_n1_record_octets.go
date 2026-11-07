package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const n1LibraryRecordLengthsID stmt.ID = "scan.inventory_accept.library_record_lengths"
const n1LibraryRecordLengthsSQL = "SELECT octet_length(id),octet_length(name),octet_length(kind),octet_length(root),octet_length(preference_local_tags),octet_length(preference_lyrics_source),octet_length(preference_title_source),octet_length(preference_episode_numbering),octet_length(playback_origin_incarnation),octet_length(metadata_language) FROM libraries WHERE id=?"

// One domain-owned, complete-PK point. Generic lint, other IDs and every
// expression outside this literal projection still refuse octet_length.
func init() {
	pointOctets.add(octetShape{
		id: n1LibraryRecordLengthsID, sql: n1LibraryRecordLengthsSQL,
		mismatch: "exact N1 library record byte point required",
		definition: func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && d.Plan.Hot &&
				slices.Equal(d.Params, []stmt.Param{{Name: "library_key", Type: stmt.Text}}) &&
				slices.Equal(d.Plan.Indexes, []string{"sqlite_autoindex_libraries_1"}) &&
				slices.Equal(d.Plan.Seeks, []string{"id=?"}) &&
				len(d.Plan.PrimaryKeys) == 0 && len(d.Plan.AutoIndexes) == 0 &&
				len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 &&
				d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
		},
		audit: auditN1LibraryRecordKey,
		point: func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH libraries USING INDEX sqlite_autoindex_libraries_1 (id=?)" {
				return fmt.Errorf("exact N1 library PRIMARY KEY point SEARCH required")
			}
			return nil
		},
	})
}

func auditN1LibraryRecordKey(ctx context.Context, conn *sql.Conn) error {
	if err := auditClosedOctetKey(ctx, conn, "libraries", "sqlite_autoindex_libraries_1", []string{"id"}); err != nil {
		return err
	}
	info, err := query(ctx, conn, "PRAGMA main.index_xinfo(sqlite_autoindex_libraries_1)")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("N1 library point: native key metadata")
		}
		if row[5] == int64(1) {
			if row[2] != "id" || row[3] != int64(0) || row[4] != "BINARY" {
				return fmt.Errorf("N1 library point: exact BINARY ASC id key required")
			}
			keys++
		}
	}
	if keys != 1 {
		return fmt.Errorf("N1 library point: one native key required")
	}
	return nil
}
