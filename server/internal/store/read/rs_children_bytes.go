package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func init() {
	for _, family := range []string{"album", "book"} {
		registerRSBytePoint("rs.children."+family+".sort_key", rsBytePoint{lengths: "rs.children." + family + ".sort_octets", columns: []int{0}})
	}
}
func rsChildSortDefinitions() []stmt.Definition {
	var out []stmt.Definition
	params := []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "parent", Type: stmt.Integer}}
	for _, family := range []string{"album", "book"} {
		item, parent := int64(4001), int64(2001)
		if family == "book" {
			item, parent = 5001, 2002
		}
		for _, point := range []struct{ name, projection string }{{"sort_key", "e.sort_key"}, {"sort_octets", "octet_length(e.sort_key)"}} {
			sql := "SELECT " + point.projection + " FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e." + family + "_id=?"
			out = append(out, stmt.Definition{ID: readStatementID("rs.children." + family + "." + point.name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_children.go:RSChildrenReader.Window"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{item, int64(1), parent}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), parent}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}})
		}
	}
	return out
}
