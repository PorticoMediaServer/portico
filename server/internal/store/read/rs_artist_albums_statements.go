package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RSAudioRelationDefinitions supplies bounded enumeration points only. These
// points do not authorize public output: featured songs need current item
// visibility, and albums need their own current container policy proof.
func RSAudioRelationDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: readStatementID("rs.audio." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_artist_albums_statements.go:RSAudioRelationDefinitions"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("primary", `SELECT id FROM catalog_entities INDEXED BY catalog_entities_artist WHERE artist_id IS NOT NULL AND artist_id=? AND id>? AND kind=6 AND retired=0 ORDER BY id LIMIT 1`, []stmt.Param{i("artist"), i("after")}, []any{int64(2006), int64(0)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_artist"}, Seeks: []string{"id>?"}})
	// Enumerate the reverse relation, then point-read the CURRENT song. Never
	// apply LIMIT to an album union before deduplicating authorized album IDs.
	add("featured", `SELECT song_id FROM catalog_song_artists INDEXED BY catalog_song_artists_artist WHERE artist_id=? AND song_id>? ORDER BY song_id LIMIT 1`, []stmt.Param{i("artist"), i("after")}, []any{int64(2006), int64(0)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_song_artists_artist"}, Seeks: []string{"song_id>?"}})
	add("song", `SELECT id,library_id,kind,retired,album_id,admission_own_age,revision,music_revision,music_membership_revision FROM catalog_entities WHERE id=? AND kind=7`, []stmt.Param{i("song")}, []any{int64(4001)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	return append(out, rsAuthorBooksDefinitions()...)
}
