package read

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

type rbGroupKey struct {
	sql  string
	text bool
}
type rbGroupSpec struct {
	family            RBGroupFamily
	name, from, where string
	index             string
	keys              []rbGroupKey
	subjectKind       int64
	kinds             []int64
	ordered           bool
}

func rbGroupSpecs() []rbGroupSpec {
	n := func(s string) rbGroupKey { return rbGroupKey{sql: s} }
	s := func(s string) rbGroupKey { return rbGroupKey{sql: s, text: true} }
	entity := "catalog_entities AS e INDEXED BY "
	return []rbGroupSpec{
		{RBGroupShowSeasons, "show-seasons", entity + "catalog_entities_season_number", "kind=3 AND e.show_id=?", "catalog_entities_season_number", []rbGroupKey{n("e.season_number"), n("e.id")}, 2, []int64{3}, false},
		{RBGroupShowEpisodes, "show-episodes", entity + "catalog_entities_show_fk", "show_id IS NOT NULL AND e.show_id=?", "catalog_entities_show_fk", []rbGroupKey{n("e.id")}, 2, []int64{4}, false},
		{RBGroupSeasonEpisodes, "season-episodes", entity + "catalog_entities_season", "season_id IS NOT NULL AND e.season_id=?", "catalog_entities_season", []rbGroupKey{n("e.episode_number"), n("e.id")}, 3, []int64{4}, false},
		{RBGroupArtistAlbums, "artist-albums", entity + "catalog_entities_artist", "artist_id IS NOT NULL AND e.artist_id=?", "catalog_entities_artist", []rbGroupKey{n("e.id")}, 5, []int64{6}, false},
		{RBGroupAlbumSongs, "album-songs", entity + "catalog_entities_album", "album_id IS NOT NULL AND e.album_id=?", "catalog_entities_album", []rbGroupKey{n("COALESCE(e.disc_number,1)"), n("COALESCE(e.track_number,0)"), n("e.id")}, 6, []int64{7}, true},
		{RBGroupAuthorBooks, "author-books", entity + "catalog_entities_book_author", "kind=8 AND e.library_id=? AND e.author COLLATE NOCASE=?", "catalog_entities_book_author", []rbGroupKey{n("e.id")}, 13, []int64{8}, false},
		{RBGroupBookParts, "book-parts", entity + "catalog_entities_book", "book_id IS NOT NULL AND e.book_id=?", "catalog_entities_book", []rbGroupKey{n("COALESCE(e.disc_number,1)"), n("COALESCE(e.part_number,0)"), n("e.id")}, 8, []int64{9}, true},
		{RBGroupCollectionMembers, "collection-members", "catalog_collection_members AS m INDEXED BY catalog_collection_members_page CROSS JOIN catalog_entities AS e ON e.id=m.item_id", "m.collection_id=?", "catalog_collection_members_page", []rbGroupKey{s("m.order_key"), n("m.item_id")}, 10, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13}, false},
		{RBGroupPlaylistEntries, "playlist-entries", "catalog_playlist_entries AS m INDEXED BY catalog_playlist_entries_page LEFT JOIN catalog_entities AS e ON e.id=m.item_id", "m.playlist_id=?", "catalog_playlist_entries_page", []rbGroupKey{s("m.order_key"), n("m.id")}, 0, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13}, false},
		{RBGroupArtistCredits, "artist-credits", "catalog_song_artists AS m INDEXED BY catalog_song_artists_artist CROSS JOIN catalog_entities AS e ON e.id=m.song_id", "m.artist_id=?", "catalog_song_artists_artist", []rbGroupKey{n("m.song_id")}, 5, []int64{7}, false},
	}
}
func rbGroupSpecFor(f RBGroupFamily) (rbGroupSpec, bool) {
	for _, s := range rbGroupSpecs() {
		if s.family == f {
			return s, true
		}
	}
	return rbGroupSpec{}, false
}
func rbGroupKind(s rbGroupSpec, k int64) bool {
	for _, kind := range s.kinds {
		if kind == k {
			return true
		}
	}
	return false
}

// RBGroupDefinitions is an inert, independently receipted harness inventory.
// Requests cannot supply SQL, identifiers, operators or policy purposes.
func RBGroupDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present, empty []any, p stmt.Plan) {
		out = append(out, stmt.Definition{ID: statementID("rb.group." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_group.go:BrowseReader.RBGroupWindow"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("subject", `SELECT id,library_id,kind,retired,public_id,metadata_incarnation,revision,source_revision,identity_revision,metadata_revision,tv_hierarchy_revision,music_membership_revision,collection_revision,title FROM catalog_entities WHERE id=?`, []stmt.Param{i("subject")}, []any{int64(2001)}, []any{int64(-1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	add("playlist", `SELECT id,owner_authority,owner_account,owner_profile,revision,deleted,token FROM catalog_playlists WHERE id=?`, []stmt.Param{i("subject")}, []any{int64(1)}, []any{int64(-1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_playlists"}})
	for _, spec := range rbGroupSpecs() {
		for _, desc := range []bool{false, true} {
			direction, op, variant := "ASC", ">", "asc."
			if desc {
				direction, op, variant = "DESC", "<", "desc."
			}
			var columns, order []string
			for keyIndex, k := range spec.keys {
				alias := fmt.Sprintf("k%d", keyIndex)
				columns = append(columns, k.sql+" AS "+alias)
				order = append(order, alias+" "+direction)
			}
			base := `SELECT e.id,'' AS unused,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind,` + strings.Join(columns, ",") + " FROM " + spec.from + " WHERE " + spec.where
			end := " ORDER BY " + strings.Join(order, ",") + " LIMIT ?"
			params := []stmt.Param{i("subject")}
			subject := rbGroupFixtureSubject(spec.family)
			bindings := []any{subject}
			if spec.family == RBGroupAuthorBooks {
				params = []stmt.Param{i("library"), text("author")}
				bindings = []any{int64(1), "Author"}
			}
			plan := stmt.Plan{Hot: true, Indexes: []string{spec.index}}
			if spec.family == RBGroupCollectionMembers || spec.family == RBGroupPlaylistEntries || spec.family == RBGroupArtistCredits {
				plan.PrimaryKeys = []string{"e"}
			}
			present := append(append([]any(nil), bindings...), int64(MaxRaw))
			empty := append([]any(nil), present...)
			empty[0] = int64(-1)
			add(spec.name+"."+variant+"first", base+end, append(append([]stmt.Param(nil), params...), i("limit")), present, empty, plan)
			for level := range spec.keys {
				var predicates []string
				pp := append([]stmt.Param(nil), params...)
				bb := append([]any(nil), bindings...)
				keys := rbGroupFixtureKeys(spec.family, desc, level)
				for k := 0; k <= level; k++ {
					operator := "="
					if k == level {
						operator = op
					}
					predicates = append(predicates, spec.keys[k].sql+operator+"?")
					param := i(fmt.Sprintf("key%d", k))
					if spec.keys[k].text {
						param = text(fmt.Sprintf("key%d", k))
					}
					pp = append(pp, param)
					bb = append(bb, keys[k])
				}
				pp = append(pp, i("limit"))
				bb = append(bb, int64(MaxRaw))
				empty := append([]any(nil), bb...)
				empty[0] = int64(-1)
				seekName := fmt.Sprintf("%s.%sseek%d", spec.name, variant, level)
				seekPlan := plan
				if spec.family == RBGroupAlbumSongs || spec.family == RBGroupBookParts {
					scope := "album_id"
					if spec.family == RBGroupBookParts {
						scope = "book_id"
					}
					constraints := []string{scope + "=?"}
					for k := 0; k <= level; k++ {
						name := "<expr>"
						if k == 2 {
							name = "id"
						}
						operator := "="
						if k == level {
							operator = op
						}
						constraints = append(constraints, name+operator+"?")
					}
					// The registry's generic EQP grammar does not recognize
					// SQLite's <expr> token. Pin ONLY this full SEARCH detail;
					// no SCAN/sort/SQL-grammar permission is introduced.
					seekPlan.Waivers = []stmt.Waiver{{StatementID: statementID("rb.group." + seekName), Stage: "eqp", Form: "UNRECOGNIZED EQP", Match: "SEARCH e USING INDEX " + spec.index + " (" + strings.Join(constraints, " AND ") + ")", Reason: "Pinned SEARCH over the canonical disc/track or disc/part expression index; exact equality prefixes and strict range, no scan or temporary sort"}}
				}
				seekEnd := " ORDER BY " + strings.Join(order[level:], ",") + " LIMIT ?"
				add(seekName, base+" AND "+strings.Join(predicates, " AND ")+seekEnd, pp, bb, empty, seekPlan)
			}
		}
	}
	return append(out, rbRelationshipDefinitions()...)
}

// Fixed cohort anchors belong to the bounded group journal; they are not request
// defaults. Every seek cohort is checked populated by the runner capture test.
func rbGroupFixtureSubject(f RBGroupFamily) int64 {
	switch f {
	case RBGroupShowSeasons, RBGroupShowEpisodes:
		return 2001
	case RBGroupSeasonEpisodes:
		return 2002
	case RBGroupArtistAlbums, RBGroupArtistCredits:
		return 2003
	case RBGroupAlbumSongs:
		return 2004
	case RBGroupAuthorBooks:
		return 2005
	case RBGroupBookParts:
		return 2006
	case RBGroupCollectionMembers:
		return 2007
	case RBGroupPlaylistEntries:
		return 1
	}
	return 0
}
func rbGroupFixtureKeys(f RBGroupFamily, desc bool, level int) []any {
	low, high := int64(-100), int64(999999)
	value := low
	if desc {
		value = high
	}
	switch f {
	case RBGroupAlbumSongs, RBGroupBookParts:
		if level == 0 {
			return []any{value}
		}
		if level == 1 {
			return []any{int64(1), value}
		}
		return []any{int64(1), int64(0), value}
	case RBGroupSeasonEpisodes, RBGroupShowSeasons:
		if level == 0 {
			return []any{value}
		}
		return []any{int64(1), value}
	case RBGroupCollectionMembers, RBGroupPlaylistEntries:
		if level == 0 {
			if desc {
				return []any{"z"}
			}
			return []any{""}
		}
		return []any{"b", value}
	}
	return []any{value}
}
