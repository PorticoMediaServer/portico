package read

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Inert definitions for collection/playlist groupOrder lift preparation. The
// caller must Capture the actual Reader before use. Authored membership windows
// use the collection page / playlist page indexes exactly as the bounded group
// windows do; lifting one raw member to the requested kind follows the accepted
// entity parent chain with complete entity primary-key points. The merged RS
// complete authored-collection source/eligibility statements are dependencies,
// not a second authority or a copied reducer.
func RBGroupLiftDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbGroupLiftDefinitions()...)
}

func rbGroupLiftDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present, empty []any, plan stmt.Plan) {
		out = append(out, stmt.Definition{
			ID:        readStatementID("rb.group_lift." + name),
			SQL:       sql,
			Owner:     "store/read",
			CallSites: []string{"store/read/rb_group_lift.go:RBGroupLiftPreparation"},
			Kind:      stmt.Read,
			Params:    params,
			Cohorts:   []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}},
			Plan:      plan,
		})
	}

	// The authored collection root and the merged RS complete collection source
	// are the same accepted current tuple. Automatic identity refuses separately
	// (RS automatic membership is not landed), never as an invented empty set.
	add("collection_source", rsCollectionCompleteSourceSQL, []stmt.Param{i("library"), i("collection")}, []any{int64(1), int64(7000)}, []any{int64(999999), int64(7000)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id=?"}})
	add("playlist", `SELECT id,owner_authority,owner_account,owner_profile,revision,deleted,token FROM catalog_playlists WHERE id=?`, []stmt.Param{i("playlist")}, []any{int64(1)}, []any{int64(999999)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_playlists"}})
	add("automatic", `SELECT 1 FROM catalog_automatic_collections INDEXED BY catalog_automatic_collection_entity WHERE entity_id=? LIMIT 1`, []stmt.Param{i("item")}, []any{int64(7001)}, []any{int64(999999)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_automatic_collection_entity"}})

	// The lift follows the authored parent link only (episode -> season -> show,
	// song -> album, book part -> book); each hop is a complete entity primary-key
	// point and the item kind/container is the accepted RS member tuple.
	entity := "e.id,e.kind,e.library_id,e.retired,e.admission_own_age,e.parent_id"
	level1 := "p1.id,p1.kind,p1.library_id,p1.retired,p1.admission_own_age,p1.parent_id"
	level2 := "p2.id,p2.kind,p2.library_id,p2.retired,p2.admission_own_age"
	collectionRows := "SELECT m.item_id,m.order_key," + entity + "," + level1 + "," + level2 + `,k.container FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page
 LEFT JOIN catalog_entities AS e ON e.id=m.item_id
 LEFT JOIN catalog_kinds AS k ON k.id=e.kind
 LEFT JOIN catalog_entities AS p1 ON p1.id=e.parent_id
 LEFT JOIN catalog_entities AS p2 ON p2.id=p1.parent_id`
	playlistRows := "SELECT entry.id,entry.item_id,entry.order_key," + entity + "," + level1 + "," + level2 + `,k.container FROM catalog_playlist_entries AS entry INDEXED BY catalog_playlist_entries_page
 LEFT JOIN catalog_entities AS e ON e.id=entry.item_id
 LEFT JOIN catalog_kinds AS k ON k.id=e.kind
 LEFT JOIN catalog_entities AS p1 ON p1.id=e.parent_id
 LEFT JOIN catalog_entities AS p2 ON p2.id=p1.parent_id`

	for _, v := range []struct {
		name, sql, subject, seek string
		params                   []stmt.Param
	}{
		{"collection.first", collectionRows + ` WHERE m.collection_id=? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=?", "collection_id=?", []stmt.Param{i("collection"), i("limit")}},
		{"collection.tail", collectionRows + ` WHERE m.collection_id=? AND m.order_key=? AND m.item_id>? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=?", "collection_id=? AND order_key=? AND item_id>?", []stmt.Param{i("collection"), s("key"), i("item"), i("limit")}},
		{"collection.next", collectionRows + ` WHERE m.collection_id=? AND m.order_key>? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=?", "collection_id=? AND order_key>?", []stmt.Param{i("collection"), s("key"), i("limit")}},
		{"playlist.first", playlistRows + ` WHERE entry.playlist_id=? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=?", "playlist_id=?", []stmt.Param{i("playlist"), i("limit")}},
		{"playlist.tail", playlistRows + ` WHERE entry.playlist_id=? AND entry.order_key=? AND entry.id>? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=?", "playlist_id=? AND order_key=? AND id>?", []stmt.Param{i("playlist"), s("key"), i("entry"), i("limit")}},
		{"playlist.next", playlistRows + ` WHERE entry.playlist_id=? AND entry.order_key>? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=?", "playlist_id=? AND order_key>?", []stmt.Param{i("playlist"), s("key"), i("limit")}},
	} {
		present := rbGroupLiftPresentBindings(v.name)
		empty := append([]any(nil), present...)
		empty[0] = int64(999999)
		plan := stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, Seeks: []string{v.seek}, PrimaryKeys: []string{"e", "k", "p1", "p2"}}
		if v.name[:8] == "playlist" {
			plan = stmt.Plan{Hot: true, Indexes: []string{"catalog_playlist_entries_page"}, Seeks: []string{v.seek}, PrimaryKeys: []string{"e", "k", "p1", "p2"}}
		}
		add(v.name, v.sql, v.params, present, empty, plan)
	}
	// The same-snapshot octet preflight uses the SAME authored order and bound;
	// only the variable order key is measured before its payload is copied.
	collectionLengths := `SELECT m.item_id,octet_length(m.order_key) FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page`
	playlistLengths := `SELECT entry.id,octet_length(entry.order_key) FROM catalog_playlist_entries AS entry INDEXED BY catalog_playlist_entries_page`
	for _, v := range []struct {
		name, sql, seek string
		params          []stmt.Param
	}{
		{"collection.first_octets", collectionLengths + ` WHERE m.collection_id=? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=?", []stmt.Param{i("collection"), i("limit")}},
		{"collection.tail_octets", collectionLengths + ` WHERE m.collection_id=? AND m.order_key=? AND m.item_id>? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=? AND order_key=? AND item_id>?", []stmt.Param{i("collection"), s("key"), i("item"), i("limit")}},
		{"collection.next_octets", collectionLengths + ` WHERE m.collection_id=? AND m.order_key>? ORDER BY m.order_key,m.item_id LIMIT ?`, "collection_id=? AND order_key>?", []stmt.Param{i("collection"), s("key"), i("limit")}},
		{"playlist.first_octets", playlistLengths + ` WHERE entry.playlist_id=? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=?", []stmt.Param{i("playlist"), i("limit")}},
		{"playlist.tail_octets", playlistLengths + ` WHERE entry.playlist_id=? AND entry.order_key=? AND entry.id>? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=? AND order_key=? AND id>?", []stmt.Param{i("playlist"), s("key"), i("entry"), i("limit")}},
		{"playlist.next_octets", playlistLengths + ` WHERE entry.playlist_id=? AND entry.order_key>? ORDER BY entry.order_key,entry.id LIMIT ?`, "playlist_id=? AND order_key>?", []stmt.Param{i("playlist"), s("key"), i("limit")}},
	} {
		present := rbGroupLiftPresentBindings(v.name)
		empty := append([]any(nil), present...)
		empty[0] = int64(999999)
		index := "catalog_collection_members_page"
		if v.name[:8] == "playlist" {
			index = "catalog_playlist_entries_page"
		}
		add(v.name, v.sql, v.params, present, empty, stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{v.seek}})
	}
	return append(out, rsDetailEligibilityDefinitions()...)
}

// rbGroupLiftPresentBindings returns populated Capture bindings for one window
// in the lift plan fixture. They are cohort anchors, never request defaults.
func rbGroupLiftPresentBindings(name string) []any {
	collection := strings.HasPrefix(name, "collection")
	subject := int64(1)
	limit := int64(rbGroupLiftPlaylistRaw + 1)
	if collection {
		subject = 7000
		limit = int64(rbGroupLiftCollectionRaw + 1)
	}
	switch name {
	case "collection.first", "collection.first_octets", "playlist.first", "playlist.first_octets":
		return []any{subject, limit}
	case "collection.tail", "collection.tail_octets":
		return []any{int64(7000), "a0000", int64(0), limit}
	case "collection.next", "collection.next_octets":
		return []any{int64(7000), "", limit}
	case "playlist.tail", "playlist.tail_octets":
		return []any{int64(1), "k0000", int64(0), limit}
	case "playlist.next", "playlist.next_octets":
		return []any{int64(1), "", limit}
	}
	return []any{subject, limit}
}
