package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const iaLinkOctetsID stmt.ID = "read.foreground.ia.link_octets"

// These media families hydrate the same native link payload. One compiled SQL
// point and ID own the byte preflight; family captures retain their own actual
// present/empty populations. No SQL alias or permissive registry is introduced.
func iaLinkOctetsDefinition(args []any) stmt.Definition {
	empty := append([]any(nil), args...)
	empty[0] = int64(9999999)
	return stmt.Definition{
		ID: iaLinkOctetsID, Kind: stmt.Read, Owner: "store/read/ia", CallSites: []string{"store/read/ia_tv.go", "store/read/ia_audio.go"},
		SQL:     "SELECT octet_length(edition),octet_length(playback_origin_incarnation) FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?",
		Params:  []stmt.Param{{Name: "entity", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}},
	}
}

// IAHarnessDefinitions composes the closed accepted-membership families. Their
// sole shared point is retained once. This is inert harness data, never product
// wiring, registration authority or a general definition deduplicator.
func IAHarnessDefinitions() []stmt.Definition {
	definitions := IAAudioHarnessDefinitions()
	for _, family := range [][]stmt.Definition{IATVDefinitions(), IAExtraHarnessDefinitions()} {
		for _, d := range family {
			if d.ID != iaLinkOctetsID {
				definitions = append(definitions, d)
			}
		}
	}
	return definitions
}
