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
	return out
}

// RSArtistAlbumDefinitions adds current primary-artist liveness, the legacy
// release-order key and the visible-member proof to the union registry; callers
// compose it with the existing common/Facts/art definitions.
func RSArtistAlbumDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return append(RSAudioRelationDefinitions(),
		// Year and title are the album entity's current legacy ordering key.
		// The new embedded schema has no legacy catalog_albums side table; the
		// primary-artist relation lives on the album entity, so that tuple is
		// this contract's catalog_albums equivalent (task 11 owns richer roles).
		stmt.Definition{ID: readStatementID("rs.audio.album_artist"), SQL: `SELECT a.artist_id,a.year,a.title FROM catalog_entities AS a CROSS JOIN catalog_entities AS ar ON ar.id=a.artist_id AND ar.kind=5 AND ar.retired=0 WHERE a.id=? AND a.library_id=? AND a.kind=6 AND a.retired=0`, Params: []stmt.Param{{Name: "album", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_artist_albums_reader.go:enumerate"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(9001), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "ar"}}},
		// One current visible member under the accepted scalar item band. This is
		// the read-side equivalent of legacy listeningSectionVisibility plus
		// EXISTS(catalog_songs): unknown year is not a member witness.
		stmt.Definition{ID: readStatementID("rs.audio.album_member"), SQL: `SELECT id FROM catalog_entities INDEXED BY catalog_entities_album WHERE album_id IS NOT NULL AND album_id=? AND library_id=? AND kind=7 AND retired=0 AND COALESCE(admission_own_age,?)<=? LIMIT 1`, Params: []stmt.Param{i("album"), i("library"), i("unrated_age"), i("visible_age_cap")}, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_artist_albums_reader.go:enumerate"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(2001), int64(1), int64(0), int64(1 << 62)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(0), int64(1 << 62)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_album"}, Seeks: []string{"album_id=?"}}})
}
