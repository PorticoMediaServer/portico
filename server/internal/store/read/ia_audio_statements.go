package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const iaAudioEntitySQL = `SELECT id,sort_key,public_id,title,retired,revision,admission_own_age,kind,artist_id,album_id,book_id,disc_number,track_number,part_number,metadata_incarnation,source_revision,identity_revision,metadata_revision,music_incarnation,music_revision,music_membership_revision,aggregate_revision,admission_revision,scalar_source_revision FROM catalog_entities WHERE id=? AND library_id=?`
const iaAudioEntityOctetsSQL = `SELECT octet_length(sort_key),octet_length(public_id),octet_length(title),octet_length(metadata_incarnation),octet_length(music_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`
const iaAudioLinkSQL = `SELECT asset_id,part_index,edition,available,cue_ordinal,start_seconds,end_seconds,playback_origin_incarnation,playback_origin_revision,info_pending_change FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`
const iaAudioLinkOctetsSQL = `SELECT octet_length(edition),octet_length(playback_origin_incarnation) FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`

// IAAudioHarnessDefinitions is inert harness composition, not registration or
// authorization. Capture these exact definitions on the actual reader first.
func IAAudioHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		if name == "link_octets" {
			out = append(out, iaLinkOctetsDefinition(args))
			return
		}
		empty := append([]any(nil), args...)
		empty[0] = int64(9999999)
		if len(p.Seeks) == 1 && (name == "album.tail" || name == "album.next" || name == "album.disc" || name == "book.tail" || name == "book.next" || name == "book.disc") {
			p.Waivers = []stmt.Waiver{{StatementID: readStatementID("foreground.ia.audio." + name), Stage: "eqp", Form: "UNRECOGNIZED EQP", Match: "SEARCH e USING INDEX " + p.Indexes[0] + " (" + p.Seeks[0] + ")", Reason: "Canonical audio COALESCE expression key: exact indexed SEARCH only; bounded tie, position and disc seeks. No scan or sort approved."}}
		}
		out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.audio." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read/ia_audio", CallSites: []string{"store/read/ia_audio.go:IAAudioMembership"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}
	add("entity", iaAudioEntitySQL, []stmt.Param{i("entity"), i("library")}, []any{int64(2001), int64(1)}, point)
	add("entity_octets", iaAudioEntityOctetsSQL, []stmt.Param{i("entity"), i("library")}, []any{int64(2001), int64(1)}, point)
	linkPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}
	add("link", iaAudioLinkSQL, []stmt.Param{i("entity"), i("asset"), i("library")}, []any{int64(2003), int64(1), int64(1)}, linkPoint)
	add("link_octets", iaAudioLinkOctetsSQL, []stmt.Param{i("entity"), i("asset"), i("library")}, []any{int64(2003), int64(1), int64(1)}, linkPoint)
	// These windows contain only fixed-size primitives. Raw rejected rows remain
	// in the ordered stream. The expression key is the canonical audio index key.
	fields := `e.id,e.kind,e.library_id,e.retired,e.revision,e.admission_own_age,e.artist_id,e.album_id,e.book_id,e.disc_number,e.track_number,e.part_number`
	add("artist", `SELECT `+fields+`,1,0 FROM catalog_entities AS e INDEXED BY catalog_entities_artist WHERE artist_id IS NOT NULL AND e.artist_id=? AND e.id>? ORDER BY e.id LIMIT ?`, []stmt.Param{i("parent"), i("after"), i("limit")}, []any{int64(2001), int64(0), int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_artist"}, Seeks: []string{"artist_id=? AND id>?"}})
	for _, v := range []struct {
		name, parent, order, index string
		id                         int64
	}{{"album", "album_id", "track_number", "catalog_entities_album", 2002}, {"book", "book_id", "part_number", "catalog_entities_book", 2005}} {
		base := `SELECT ` + fields + `,COALESCE(e.disc_number,1) AS disc_order,COALESCE(e.` + v.order + `,0) AS position_order FROM catalog_entities AS e INDEXED BY ` + v.index + ` WHERE ` + v.parent + ` IS NOT NULL AND e.` + v.parent + `=?`
		order := ` ORDER BY disc_order,position_order,e.id LIMIT ?`
		add(v.name+".first", base+order, []stmt.Param{i("parent"), i("limit")}, []any{v.id, int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{v.index}, Seeks: []string{v.parent + "=?"}})
		add(v.name+".tail", base+` AND COALESCE(e.disc_number,1)=? AND COALESCE(e.`+v.order+`,0)=? AND e.id>?`+` ORDER BY e.id LIMIT ?`, []stmt.Param{i("parent"), i("disc"), i("position"), i("after"), i("limit")}, []any{v.id, int64(1), int64(1), int64(0), int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{v.index}, Seeks: []string{v.parent + "=? AND <expr>=? AND <expr>=? AND id>?"}})
		add(v.name+".next", base+` AND COALESCE(e.disc_number,1)=? AND COALESCE(e.`+v.order+`,0)>?`+` ORDER BY position_order,e.id LIMIT ?`, []stmt.Param{i("parent"), i("disc"), i("position"), i("limit")}, []any{v.id, int64(1), int64(1), int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{v.index}, Seeks: []string{v.parent + "=? AND <expr>=? AND <expr>>?"}})
		disc := int64(1)
		if v.name == "book" {
			disc = 0
		}
		add(v.name+".disc", base+` AND COALESCE(e.disc_number,1)>?`+order, []stmt.Param{i("parent"), i("disc"), i("limit")}, []any{v.id, disc, int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{v.index}, Seeks: []string{v.parent + "=? AND <expr>>?"}})
	}

	add("links", `SELECT asset_id,part_index FROM catalog_asset_links INDEXED BY catalog_asset_links_order WHERE entity_id=? ORDER BY part_index,asset_id LIMIT 17`, []stmt.Param{i("entity")}, []any{int64(2003)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}})
	add("song_artists", `SELECT a.artist_id,e.library_id,e.kind,e.retired,e.admission_own_age FROM catalog_song_artists AS a CROSS JOIN catalog_entities AS e ON e.id=a.artist_id WHERE a.song_id=? ORDER BY a.artist_id LIMIT 65`, []stmt.Param{i("song")}, []any{int64(2003)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "e"}, Seeks: []string{"song_id=?"}})
	return out
}
