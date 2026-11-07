package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type rsChildrenRecipe struct {
	name, index, predicate, first, second string
	parentKind, childKind                 int64
	keys                                  int
	nullable                              bool
}

func rsChildrenRecipeFor(family RSChildrenFamily) (rsChildrenRecipe, bool) {
	switch family {
	case RSAlbumTracks:
		return rsChildrenRecipe{"album", "catalog_entities_album", "album_id IS NOT NULL AND album_id=?", "COALESCE(disc_number,1)", "COALESCE(track_number,0)", 6, 7, 2, false}, true
	case RSAudiobookFiles:
		return rsChildrenRecipe{"book", "catalog_entities_book", "book_id IS NOT NULL AND book_id=?", "COALESCE(disc_number,1)", "COALESCE(part_number,0)", 8, 9, 2, false}, true
	case RSShowSeasons:
		return rsChildrenRecipe{"seasons", "catalog_entities_season_number", "kind=3 AND show_id=?", "season_number", "0", 2, 3, 1, false}, true
	case RSShowEpisodes:
		return rsChildrenRecipe{"show", "catalog_entities_show", "show_id IS NOT NULL AND show_id=? AND season_id IS NULL", "episode_number", "0", 2, 4, 1, true}, true
	case RSSeasonEpisodes:
		return rsChildrenRecipe{"season", "catalog_entities_season", "season_id IS NOT NULL AND season_id=?", "episode_number", "0", 3, 4, 1, true}, true
	case RSArtistAlbums:
		return rsChildrenRecipe{"artist", "catalog_entities_artist", "artist_id IS NOT NULL AND artist_id=?", "0", "0", 5, 6, 0, false}, true
	}
	return rsChildrenRecipe{}, false
}

func rsChildrenName(name string) string { return "foreground.rs.children." + name }

// RSChildrenDefinitions is inert qualification composition. Combine with
// HarnessDefinitions, Capture on the actual reader and pass the opaque receipt
// to stmt.New. There is no shared registry edit or runtime registration.
// Listening sort-key points use the registered actual-byte preflight pair;
// membership streams retain the existing SEARCH-only numeric indexes.
func RSChildrenDefinitions() []stmt.Definition {
	i := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present []any, index string, seeks ...string) {
		empty := append([]any(nil), present...)
		empty[0] = int64(999999)
		p := stmt.Plan{Hot: true, Seeks: seeks}
		if index == "" {
			p.PrimaryKeys = []string{"e"}
		} else {
			p.Indexes = []string{index}
		}
		// The closed HN EQP vocabulary deliberately excludes expression-index
		// wording. These exact statement-bound SEARCH-only definitions cover
		// the canonical expression seek; no SCAN or sort exception is granted.
		for _, family := range []string{"album", "book"} {
			prefix := family + "_id=?"
			var detail string
			switch name {
			case family + ".tail":
				detail = prefix + " AND <expr>=? AND <expr>=? AND id>?"
			case family + ".next_second":
				detail = prefix + " AND <expr>=? AND <expr>>?"
			case family + ".next":
				detail = prefix + " AND <expr>>?"
			}
			if detail != "" {
				p.Waivers = []stmt.Waiver{{StatementID: readStatementID(rsChildrenName(name)), Stage: "eqp", Form: "UNRECOGNIZED EQP", Match: "SEARCH catalog_entities USING INDEX " + index + " (" + detail + ")", Reason: "Canonical listening-order expression-index SEARCH; exact statement-bound wording, no scan or sort exception."}}
			}
		}
		out = append(out, stmt.Definition{ID: readStatementID(rsChildrenName(name)), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_children.go:RSChildrenReader.Window"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("source", `SELECT e.id,e.public_id,e.library_id,e.kind,e.retired,e.revision,e.admission_own_age,e.show_id,
e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,
e.tv_incarnation,e.tv_source_revision,e.tv_hierarchy_revision,e.tv_selection_revision,e.tv_descriptive_revision,
e.music_incarnation,e.music_revision,e.music_membership_revision,
e.show_settings_revision,e.hide_seasons,e.absolute_numbering,e.newest_first
FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=?`, []stmt.Param{i("parent"), i("library"), i("kind")}, []any{int64(2001), int64(1), int64(6)}, "")
	for family := RSAlbumTracks; family <= RSArtistAlbums; family++ {
		r, _ := rsChildrenRecipeFor(family)
		parent := int64(2000) + int64(family)
		base := `SELECT id,public_id,library_id,kind,retired,revision,admission_own_age,show_id,season_id,artist_id,album_id,book_id,
disc_number,track_number,part_number,season_number,episode_number,` + r.first + ` AS rs_order_first,` + r.second + ` AS rs_order_second FROM catalog_entities INDEXED BY ` + r.index + ` WHERE ` + r.predicate
		order := "id"
		if r.keys > 0 {
			order = "rs_order_first,id"
		}
		if r.keys > 1 {
			order = "rs_order_first,rs_order_second,id"
		}
		end := " ORDER BY " + order + " LIMIT ?"
		firstLimit := int64(MaxRaw)
		if r.keys == 2 {
			firstLimit = RSListeningChildCap + 1
		}
		add(r.name+".first", base+end, []stmt.Param{i("parent"), i("limit")}, []any{parent, firstLimit}, r.index)
		params := []stmt.Param{i("parent")}
		values := []any{parent}
		tail := ""
		first := i("first")
		if r.keys > 0 {
			tail += " AND " + r.first + "=?"
			params = append(params, first)
			values = append(values, int64(1))
		}
		if r.keys > 1 {
			tail += " AND " + r.second + "=?"
			params = append(params, i("second"))
			values = append(values, int64(0))
		}
		tail += " AND id>?"
		params = append(params, i("after"), i("limit"))
		values = append(values, int64(0), int64(64))
		add(r.name+".tail", base+tail+" ORDER BY id LIMIT ?", params, values, r.index, "id>?")
		if r.nullable {
			add(r.name+".tail_null", base+" AND "+r.first+" IS NULL AND id>? ORDER BY id LIMIT ?", []stmt.Param{i("parent"), i("after"), i("limit")}, []any{parent, int64(0), int64(64)}, r.index, "id>?")
		}
		if r.keys > 1 {
			add(r.name+".next_second", base+" AND "+r.first+"=? AND "+r.second+">? ORDER BY rs_order_second,id LIMIT ?", []stmt.Param{i("parent"), i("first"), i("second"), i("limit")}, []any{parent, int64(1), int64(0), int64(64)}, r.index, "<expr>>?")
		}
		if r.keys > 0 {
			seek := r.first + ">?"
			if r.keys > 1 {
				seek = "<expr>>?"
			}
			add(r.name+".next", base+" AND "+r.first+">?"+end, []stmt.Param{i("parent"), i("first"), i("limit")}, []any{parent, int64(1), int64(64)}, r.index, seek)
			if r.nullable {
				add(r.name+".next_null", base+" AND "+r.first+" IS NOT NULL"+end, []stmt.Param{i("parent"), i("limit")}, []any{parent, int64(64)}, r.index, r.first+">?")
			}
		}
	}
	return append(out, rsChildSortDefinitions()...)
}
