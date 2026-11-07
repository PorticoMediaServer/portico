package plan

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func init() {
	pointOctets.add(octetShape{"read.browse.rs.reading.octets", `SELECT octet_length(e.public_id),octet_length(e.title),octet_length(e.added_at),octet_length(e.reading_publication_type),octet_length(e.reading_publication_type_source),octet_length(e.reading_issue_date),octet_length(e.reading_issue_date_source),octet_length(e.reading_author_value),octet_length(e.reading_author_key),octet_length(e.reading_author_source),octet_length(e.reading_series_value),octet_length(e.reading_series_key),octet_length(e.reading_series_id),octet_length(e.reading_series_source) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.has_reading_attribution_members=1`, "Reading facts require the accepted exact leaf point", rsDetailOctetDefinition, auditRSDetailOctetPoint, checkRSDetailOctetPoint})
}

func init() {
	pointOctets.add(octetShape{"read.browse.rs.reading.asset_octets", `SELECT octet_length(token),octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`, "Reading inventory requires an exact asset point", func(d stmt.Definition) bool {
		return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && d.Params[1].Type == stmt.Integer && !d.Params[1].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "catalog_assets" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
	}, auditRSDetailOctetPoint, func(eqp Rows) error {
		if len(eqp.Values) != 1 || eqp.Values[0][3] != "SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)" {
			return fmt.Errorf("Reading asset requires native integer point")
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.reading.library_source_octets", `SELECT octet_length(root),octet_length(configured_root),octet_length(health),octet_length(incarnation) FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`, "Reading inventory requires an exact source point", func(d stmt.Definition) bool {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != "sqlite_autoindex_library_sources_1" || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
			return false
		}
		for _, p := range d.Params {
			if p.Type != stmt.Text || p.Nullable {
				return false
			}
		}
		pin := d.Plan.AutoIndexes[0]
		return pin.Table == "library_sources" && pin.Name == "sqlite_autoindex_library_sources_1" && pin.Origin == "pk" && len(pin.Columns) == 1 && pin.Columns[0] == (stmt.IndexColumn{Name: "id", Collation: "BINARY"})
	}, auditIssuerOctetKey, checkIssuerOctetPoint})
}

func init() {
	pointOctets.add(octetShape{"read.browse.rs.reading.asset_key_octets", `SELECT octet_length(token) FROM catalog_assets WHERE id=? AND library_id=?`, "Reading format eligibility requires an exact asset key point", rsReadingAssetKeyDefinition, auditRSDetailOctetPoint, func(eqp Rows) error {
		if len(eqp.Values) != 1 || eqp.Values[0][3] != "SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)" {
			return fmt.Errorf("exact Reading asset key required")
		}
		return nil
	}})
}
func rsReadingAssetKeyDefinition(d stmt.Definition) bool {
	return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && d.Params[1].Type == stmt.Integer && !d.Params[1].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "catalog_assets" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}
