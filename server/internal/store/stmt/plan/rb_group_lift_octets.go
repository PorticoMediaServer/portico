package plan

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Collection/playlist groupOrder lift windows measure only the authored order
// key in the SAME snapshot before its payload is copied. Each leg is an exact
// forced-index range with a bound; no cohort or scan byte point is introduced.
const (
	rbGroupLiftCollectionLengthsSQL = `SELECT m.item_id,octet_length(m.order_key) FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page`
	rbGroupLiftPlaylistLengthsSQL   = `SELECT entry.id,octet_length(entry.order_key) FROM catalog_playlist_entries AS entry INDEXED BY catalog_playlist_entries_page`
)

func init() {
	for _, shape := range []struct {
		name, sql, seek, index string
		types                  []stmt.Type
	}{
		{"collection.first_octets", rbGroupLiftCollectionLengthsSQL + ` WHERE m.collection_id=? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=?", "catalog_collection_members_page", []stmt.Type{stmt.Integer, stmt.Integer}},
		{"collection.tail_octets", rbGroupLiftCollectionLengthsSQL + ` WHERE m.collection_id=? AND m.order_key=? AND m.item_id>? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=? AND order_key=? AND item_id>?", "catalog_collection_members_page", []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer, stmt.Integer}},
		{"collection.next_octets", rbGroupLiftCollectionLengthsSQL + ` WHERE m.collection_id=? AND m.order_key>? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=? AND order_key>?", "catalog_collection_members_page", []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer}},
		{"playlist.first_octets", rbGroupLiftPlaylistLengthsSQL + ` WHERE entry.playlist_id=? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=?", "catalog_playlist_entries_page", []stmt.Type{stmt.Integer, stmt.Integer}},
		{"playlist.tail_octets", rbGroupLiftPlaylistLengthsSQL + ` WHERE entry.playlist_id=? AND entry.order_key=? AND entry.id>? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=? AND order_key=? AND id>?", "catalog_playlist_entries_page", []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer, stmt.Integer}},
		{"playlist.next_octets", rbGroupLiftPlaylistLengthsSQL + ` WHERE entry.playlist_id=? AND entry.order_key>? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=? AND order_key>?", "catalog_playlist_entries_page", []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer}},
	} {
		shape := shape
		pointOctets.add(octetShape{id: stmt.ID("read.browse.rb.group_lift." + shape.name), sql: shape.sql, mismatch: "exact group-lift authored octet leg required",
			definition: func(d stmt.Definition) bool {
				return rbGroupLiftOctetDefinition(d, shape.types, shape.index)
			},
			audit: auditRSDetailOctetPoint,
			point: func(r Rows) error {
				return checkRBGroupLiftOctetPoint(r, shape.index, shape.seek)
			},
		})
	}
}

func rbGroupLiftOctetDefinition(d stmt.Definition, types []stmt.Type, index string) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(types) || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.AutoIndexes) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != index {
		return false
	}
	for n, p := range d.Params {
		if p.Nullable || p.Type != types[n] {
			return false
		}
	}
	return true
}

func checkRBGroupLiftOctetPoint(r Rows, index, seek string) error {
	found := false
	for _, row := range r.Values {
		if len(row) != 4 {
			return fmt.Errorf("group lift: invalid native EQP")
		}
		s, ok := row[3].(string)
		if !ok || !strings.HasPrefix(s, "SEARCH ") {
			return fmt.Errorf("group lift: exact indexed source position required")
		}
		if strings.Contains(s, index+" (") && strings.Contains(s, seek) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("group lift: missing exact source seek")
	}
	return nil
}
