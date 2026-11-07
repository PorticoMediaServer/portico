package plan

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func init() {
	pointOctets.add(octetShape{stmt.ID("read.browse.rs.audio.author_key_octets"), `SELECT octet_length(lower(trim(e.title))) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=13 AND e.retired=0`, "listening author13 normalization requires an exact entity point", func(d stmt.Definition) bool {
		return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && d.Params[1].Type == stmt.Integer && !d.Params[1].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "e" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
	}, auditRSDetailOctetPoint, checkRSDetailOctetPoint})
}
