package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert definitions; the caller must Capture the actual Reader before use.
// Every source leg reuses an existing index and seek:
//
//   - title parents (albums for album order, artists for artist order) are the
//     existing dual title points, one payload and one same-snapshot octet leg
//     each, with first/equality-tail/next-key seeks;
//   - albums under an artist are the catalog_entities_artist (artist_id,id)
//     covering seek;
//   - songs under an album are the catalog_entities_album
//     (album_id,disc,track,id) covering seek, continued by the exact composite
//     cursor so no page rescans the album from its first song.
//
// No new index, table, temp sort or whole-library scan is introduced.
func RBRelationshipPrepareDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbRelationshipPrepareDefinitions()...)
}

func rbRelationshipPrepareDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args, empty []any, plan stmt.Plan) {
		out = append(out, stmt.Definition{ID: statementID("rb.relationship_prepare." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_relationship_prepare.go:RBRelationshipPreparation.window"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: plan})
	}
	limit := int64(rbRelationshipPrepareRaw + 1)
	// The song index is an expression index, so its bounded keyset EQP names
	// <expr>; waive that exact reviewed detail while pinning index and seek.
	songPlan := func(name, match string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_album"}, Seeks: []string{"album_id=?"},
			Waivers: []stmt.Waiver{{StatementID: readStatementID("rb.relationship_prepare." + name), Stage: "eqp", Form: "UNRECOGNIZED EQP", Match: match,
				Reason: "reviewed expression-index keyset for the authored album song order"}}}
	}

	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, part := range []string{"first", "tail", "next"} {
			where := " WHERE e.library_id=? AND e.kind=?"
			seek := "library_id=? AND kind=?"
			params := []stmt.Param{i("library"), i("kind")}
			args := []any{int64(1), int64(5)}
			if part == "tail" {
				where += " AND e." + key + " COLLATE NOCASE=? AND e.id>?"
				seek = key + "=? AND id>?"
				params = append(params, s("key"), i("after"))
				args = append(args, "same", int64(3))
			}
			if part == "next" {
				where += " AND e." + key + " COLLATE NOCASE>?"
				seek = key + ">?"
				params = append(params, s("key"))
				args = append(args, "same")
			}
			params = append(params, i("limit"))
			args = append(args, limit)
			end := " ORDER BY e." + key + " COLLATE NOCASE ASC,e.id ASC LIMIT ?"
			from := " FROM catalog_entities AS e INDEXED BY " + index
			columns := "e.id,e." + key + ",e.retired,e.library_id,e.kind"
			empty := append([]any(nil), args...)
			empty[0] = int64(-1)
			add("title."+variant+part, "SELECT "+columns+from+where+end, params, args, empty, stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}})
			lengths := append([]stmt.Param(nil), params...)
			add("title."+variant+part+".bytes", "SELECT e.id,octet_length(e."+key+")"+from+where+end, lengths, args, empty, stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}})
		}
	}

	child := "SELECT e.id,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind"
	add("albums.first",
		child+" FROM catalog_entities AS e INDEXED BY catalog_entities_artist WHERE artist_id IS NOT NULL AND e.artist_id=? AND e.kind=6 AND e.library_id=? ORDER BY e.id ASC LIMIT ?",
		[]stmt.Param{i("artist"), i("library"), i("limit")}, []any{int64(100000), int64(1), limit}, []any{int64(-1), int64(1), limit},
		stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_artist"}, Seeks: []string{"artist_id=?"}})
	add("albums.tail",
		child+" FROM catalog_entities AS e INDEXED BY catalog_entities_artist WHERE artist_id IS NOT NULL AND e.artist_id=? AND e.kind=6 AND e.library_id=? AND e.id>? ORDER BY e.id ASC LIMIT ?",
		[]stmt.Param{i("artist"), i("library"), i("after"), i("limit")}, []any{int64(100000), int64(1), int64(0), limit}, []any{int64(-1), int64(1), int64(0), limit},
		stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_artist"}, Seeks: []string{"artist_id=? AND id>?"}})

	song := child + ",COALESCE(e.disc_number,1) AS k0,COALESCE(e.track_number,0) AS k1"
	songsOrder := " ORDER BY k0 ASC,k1 ASC,e.id ASC LIMIT ?"
	songsWhere := " FROM catalog_entities AS e INDEXED BY catalog_entities_album WHERE album_id IS NOT NULL AND e.album_id=? AND e.kind=7 AND e.library_id=?"
	add("songs.first",
		song+songsWhere+songsOrder,
		[]stmt.Param{i("album"), i("library"), i("limit")}, []any{int64(200000), int64(1), limit}, []any{int64(-1), int64(1), limit},
		songPlan("songs.first", "SEARCH e USING INDEX catalog_entities_album (album_id=?)"))
	add("songs.tail",
		song+songsWhere+" AND COALESCE(e.disc_number,1)=? AND COALESCE(e.track_number,0)=? AND e.id>? ORDER BY e.id ASC LIMIT ?",
		[]stmt.Param{i("album"), i("library"), i("disc"), i("track"), i("after"), i("limit")}, []any{int64(200000), int64(1), int64(1), int64(0), int64(0), limit}, []any{int64(-1), int64(1), int64(1), int64(0), int64(0), limit},
		songPlan("songs.tail", "SEARCH e USING INDEX catalog_entities_album (album_id=? AND <expr>=? AND <expr>=? AND id>?)"))
	add("songs.next_track",
		song+songsWhere+" AND COALESCE(e.disc_number,1)=? AND COALESCE(e.track_number,0)>? ORDER BY k1 ASC,e.id ASC LIMIT ?",
		[]stmt.Param{i("album"), i("library"), i("disc"), i("track"), i("limit")}, []any{int64(200000), int64(1), int64(1), int64(0), limit}, []any{int64(-1), int64(1), int64(1), int64(0), limit},
		songPlan("songs.next_track", "SEARCH e USING INDEX catalog_entities_album (album_id=? AND <expr>=? AND <expr>>?)"))
	add("songs.next",
		song+songsWhere+" AND COALESCE(e.disc_number,1)>?"+songsOrder,
		[]stmt.Param{i("album"), i("library"), i("disc"), i("limit")}, []any{int64(200000), int64(1), int64(1), limit}, []any{int64(-1), int64(1), int64(1), limit},
		songPlan("songs.next", "SEARCH e USING INDEX catalog_entities_album (album_id=? AND <expr>>?)"))

	return out
}
