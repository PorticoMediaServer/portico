package read

import (
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RBFilterMembershipDefinitions is inert compiled inventory for the closed
// membership and contextual-source filter primitive: collection, memberOf,
// savedCollection, list, album, artist and the episode-group source. Every
// request value is a binding: request fields cannot select SQL, an identifier,
// an operator or a source.
//
// A fact window starts at the selected groups and is bounded by the page's raw
// title identities (a fixed 64-slot list) so a large collection, playlist or
// container can never enumerate the library. Unused slots bind -1 (or the empty
// text/blob), which can never equal a positive identity.
const (
	rbMembershipSlot      = MaxRaw
	rbMembershipValueSlot = 100
	// rbMembershipFactLimit bounds one fact window's rows. Reaching it is
	// incomplete evidence, never a sampled exact result.
	rbMembershipFactLimit = 2048
	// rbMembershipTitleValues bounds one title's fact values. Exceeding it is
	// incomplete evidence for that title, never a truncated answer.
	rbMembershipTitleValues = 64
)

func rbMembershipIDParams() []stmt.Param {
	out := make([]stmt.Param, rbMembershipSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "title" + strconv.Itoa(i), Type: stmt.Integer}
	}
	return out
}

func rbMembershipBlobParams() []stmt.Param {
	out := make([]stmt.Param, rbMembershipValueSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "value" + strconv.Itoa(i), Type: stmt.Blob}
	}
	return out
}

func rbMembershipTextParams() []stmt.Param {
	out := make([]stmt.Param, rbMembershipValueSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "value" + strconv.Itoa(i), Type: stmt.Text}
	}
	return out
}

func rbMembershipMarks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func rbMembershipIDs() string        { return rbMembershipMarks(rbMembershipSlot) }
func rbMembershipValues() string     { return rbMembershipMarks(rbMembershipValueSlot) }

func rbMembershipAdmissionParams() []stmt.Param {
	return []stmt.Param{
		{Name: "allow_unrated", Type: stmt.Integer},
		{Name: "maximum_age_lo", Type: stmt.Integer},
		{Name: "maximum_age_hi", Type: stmt.Integer},
	}
}

// rbMembershipAdmissionSQL is the legacy item restriction over a member item
// (identity scope, never the container's own age). me/ml are the member entity
// and its library. A retired member or a retired member library never matches.
const rbMembershipAdmissionSQL = ` me.retired=0 AND ml.retired=0 AND (CASE WHEN me.admission_own_age IS NULL THEN ? ELSE CASE WHEN ?<0 THEN 1 ELSE me.admission_own_age<=? END END)<>0`

const rbMembershipCall = "store/read/rb_filter_membership.go:BrowseReader.MembershipFilterWindow"

// rbMembershipFactShape pairs a fact payload statement with its exact octet
// preflight. Both use the same params, source and predicate; only the value
// projection differs.
func rbMembershipFactShape(name, columns, bytesColumns, from, where string, params []stmt.Param, present, empty []any, plan stmt.Plan) []stmt.Definition {
	base := readStatementID("rb.filter_membership." + name)
	sql := "SELECT " + columns + " FROM " + from + " WHERE " + where + " LIMIT ?"
	bytesSQL := "SELECT " + bytesColumns + " FROM " + from + " WHERE " + where + " LIMIT ?"
	return []stmt.Definition{
		{ID: base, SQL: sql, Owner: "store/read", Kind: stmt.Read, CallSites: []string{rbMembershipCall}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: plan},
		{ID: readStatementID("rb.filter_membership." + name + ".bytes"), SQL: bytesSQL, Owner: "store/read", Kind: stmt.Read, CallSites: []string{rbMembershipCall}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: plan},
	}
}

// RBFilterMembershipDefinitions is the complete inert inventory. Only these
// exact statements may run; the plan Capture owns their native receipts.
func RBFilterMembershipDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	var defs []stmt.Definition

	// Raw title walk: accepted article-aware title order over the requested
	// library and kind, exactly like the existing filter windows.
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, desc := range []bool{false, true} {
			direction, op, order := "asc.", ">", "ASC"
			if desc {
				direction, op, order = "desc.", "<", "DESC"
			}
			columns := "e.id,e." + key + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.show_id,e.season_id,e.credit_image_id,e.credit_owner_revision,e.metadata_incarnation"
			from := "catalog_entities AS e INDEXED BY " + index
			end := " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
			base := "SELECT " + columns + " FROM " + from + " WHERE e.library_id=? AND e.kind=?"
			bytesSQL := "SELECT e.id,octet_length(e." + key + "),octet_length(e.title) FROM " + from + " WHERE e.library_id=? AND e.kind=?"
			add := func(part, where, seek string, params []stmt.Param, bindings []any) {
				empty := append([]any(nil), bindings...)
				empty[0] = int64(-1)
				name := "rb.filter_membership.entity." + variant + direction + part
				proof := stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
				defs = append(defs,
					stmt.Definition{ID: readStatementID(name), SQL: base + where + end, Owner: "store/read", Kind: stmt.Read, CallSites: []string{rbMembershipCall}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: proof},
					stmt.Definition{ID: readStatementID(name + ".bytes"), SQL: bytesSQL + where + end, Owner: "store/read", Kind: stmt.Read, CallSites: []string{rbMembershipCall}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: proof},
				)
			}
			add("first", "", "library_id=? AND kind=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(MaxRaw)})
			add("tail", " AND e."+key+" COLLATE NOCASE=? AND e.id"+op+"?", key+"=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")}, []any{int64(1), int64(1), "same", int64(3), int64(MaxRaw)})
			add("next", " AND e."+key+" COLLATE NOCASE"+op+"?", key+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("limit")}, []any{int64(1), int64(1), "same", int64(MaxRaw)})
		}
	}

	admission := rbMembershipAdmissionParams()
	admissionPresent := []any{int64(1), int64(-1), int64(-1)}
	admissionEmpty := []any{int64(0), int64(0), int64(0)}
	idsPresent := rbMembershipTitleBindings(1)
	idsEmpty := rbMembershipEmptyTitles()
	limitParam := i("limit")

	idParams := rbMembershipIDParams()

	// A selected collection's members, lifted to every browse entity that holds
	// the member (self plus its containers), restricted to this page's titles.
	// catalog_collection_members_item drives the member side; the membership
	// item index drives the lift.
	collectionFrom := "catalog_entities AS c CROSS JOIN catalog_collection_members AS cm ON cm.collection_id=c.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=cm.item_id CROSS JOIN catalog_entities AS me ON me.id=cm.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	collectionWhere := "c.public_id IN(" + rbMembershipValues() + ") AND m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	collectionParams := append(append(append(rbMembershipBlobParams(), idParams...), admission...), limitParam)
	collectionPresent := append(append(append(rbMembershipBlobBindings(20), rbMembershipTitleBindings(1)...), admissionPresent...), int64(rbMembershipFactLimit+1))
	collectionEmpty := append(append(append(rbMembershipBlobBindings(20), rbMembershipEmptyTitles()...), admissionEmpty...), int64(rbMembershipFactLimit+1))
	collectionPlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"item_id=?"}}
	defs = append(defs, rbMembershipFactShape("collection",
		"m.entity_id,c.public_id", "m.entity_id,octet_length(c.public_id)", collectionFrom, collectionWhere,
		collectionParams, collectionPresent, collectionEmpty, collectionPlan)...)

	// Presence: any collection holds this title (or one of its members). The
	// public collection field allows is-present/is-missing; no selected value
	// is needed.
	presenceFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_collection_members AS cm INDEXED BY catalog_collection_members_item ON cm.item_id=m.item_id CROSS JOIN catalog_entities AS c ON c.id=cm.collection_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	presenceWhere := "m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	presenceParams := append(append(append([]stmt.Param(nil), idParams...), admission...), limitParam)
	presencePresent := append(append(append([]any(nil), idsPresent...), admissionPresent...), int64(rbMembershipFactLimit+1))
	presenceEmpty := append(append(append([]any(nil), idsEmpty...), admissionEmpty...), int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("collection.presence",
		"m.entity_id,c.public_id", "m.entity_id,octet_length(c.public_id)", presenceFrom, presenceWhere,
		presenceParams, presencePresent, presenceEmpty,
		stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}, PrimaryKeys: []string{"c", "me", "ml"}, Seeks: []string{"item_id=?"}})...)

	// A selected container's members, lifted to every browse entity that holds
	// that member. A show/season/album/artist/author container therefore matches
	// the shows/seasons/etc. that hold its members, exactly as the legacy
	// catalog_browse_memberships pivot does.
	parentFrom := "catalog_entities AS c CROSS JOIN catalog_browse_memberships AS p ON p.entity_id=c.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=p.item_id CROSS JOIN catalog_entities AS me ON me.id=p.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	parentWhere := "c.public_id IN(" + rbMembershipValues() + ") AND m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	parentParams := append(append(append(rbMembershipBlobParams(), idParams...), admission...), limitParam)
	parentPresent := append(append(append(rbMembershipBlobBindings(21), rbMembershipTitleBindings(1)...), admissionPresent...), int64(rbMembershipFactLimit+1))
	parentEmpty := append(append(append(rbMembershipBlobBindings(21), rbMembershipEmptyTitles()...), admissionEmpty...), int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("parent",
		"m.entity_id,c.public_id", "m.entity_id,octet_length(c.public_id)", parentFrom, parentWhere,
		parentParams, parentPresent, parentEmpty,
		stmt.Plan{Hot: true, Indexes: []string{"catalog_browse_membership_item"}, PrimaryKeys: []string{"p", "me", "ml"}, Seeks: []string{"entity_id=?", "item_id=?"}})...)

	// Saved personal collections: the entry itself (direct), the title that
	// holds an entry (held), and the parts of a saved container (parts). The
	// selected resource identities drive the entry order; share and owner facts
	// are the viewer's own live principal.
	savedFrom := "saved_resources AS r CROSS JOIN saved_resource_entries AS sre INDEXED BY saved_resource_entries_order ON sre.resource_id=r.id"
	savedShare := "LEFT JOIN saved_resource_shares AS sh ON sh.resource_id=r.id AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?"
	savedWhere := "r.id IN(" + rbMembershipValues() + ") AND r.kind='collection' AND r.deleted=0 AND (r.owner_key=? OR r.visibility='server' OR sh.resource_id IS NOT NULL)"
	shareParams := []stmt.Param{s("authority"), s("account"), s("profile")}
	sharePresent := []any{"local", "a", "p"}
	shareEmpty := []any{"absent", "absent", "absent"}
	ownerKeyParam := s("owner_key")
	ownerPresent := "viewer:fixture"
	ownerEmpty := "absent"
	savedParams := func(extra ...stmt.Param) []stmt.Param {
		out := append([]stmt.Param(nil), shareParams...)
		out = append(out, ownerKeyParam)
		out = append(out, rbMembershipTextParams()...)
		out = append(out, extra...)
		return append(out, limitParam)
	}
	savedBindings := func(ids []any, extra ...any) []any {
		out := append([]any(nil), sharePresent...)
		out = append(out, ownerPresent)
		out = append(out, rbMembershipTextBindings(22)...)
		out = append(out, ids...)
		out = append(out, extra...)
		return append(out, int64(rbMembershipFactLimit+1))
	}
	savedEmptyBindings := func(ids []any, extra ...any) []any {
		out := append([]any(nil), shareEmpty...)
		out = append(out, ownerEmpty)
		out = append(out, rbMembershipTextBindings(22)...)
		out = append(out, ids...)
		out = append(out, extra...)
		return append(out, int64(rbMembershipFactLimit+1))
	}

	// Direct: the title is the saved entry.
	directParams := savedParams(idParams...)
	defs = append(defs, rbMembershipFactShape("saved.direct",
		"sre.item_id,r.id", "sre.item_id,octet_length(r.id)", savedFrom+" "+savedShare, savedWhere+" AND sre.item_id IN("+rbMembershipIDs()+")",
		directParams, savedBindings(rbMembershipTitleBindings(1)), savedEmptyBindings(rbMembershipEmptyTitles()),
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}, Seeks: []string{"resource_id=?"}})...)

	// Held: the title holds a saved entry, restricted on the entry's own item.
	heldFrom := savedFrom + " CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=sre.item_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id " + savedShare
	heldWhere := savedWhere + " AND m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	heldParams := append(append(append([]stmt.Param(nil), shareParams...), ownerKeyParam), append(append(rbMembershipTextParams(), idParams...), admission...)...)
	heldParams = append(heldParams, limitParam)
	heldPresent := append(append(append(append([]any(nil), sharePresent...), ownerPresent), append(rbMembershipTextBindings(22), rbMembershipTitleBindings(1)...)...), admissionPresent...)
	heldPresent = append(heldPresent, int64(rbMembershipFactLimit+1))
	heldEmpty := append(append(append(append([]any(nil), shareEmpty...), ownerEmpty), append(rbMembershipTextBindings(22), rbMembershipEmptyTitles()...)...), admissionEmpty...)
	heldEmpty = append(heldEmpty, int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("saved.held",
		"m.entity_id,r.id", "m.entity_id,octet_length(r.id)", heldFrom, heldWhere,
		heldParams, heldPresent, heldEmpty,
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order", "catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"resource_id=?", "item_id=?"}})...)

	// Parts: the title is a member of a saved container entry.
	partsFrom := savedFrom + " CROSS JOIN catalog_browse_memberships AS m ON m.entity_id=sre.item_id " + savedShare
	partsWhere := savedWhere + " AND m.item_id IN(" + rbMembershipIDs() + ")"
	partsParams := append(append(append([]stmt.Param(nil), shareParams...), ownerKeyParam), append(append(rbMembershipTextParams(), idParams...))...)
	partsParams = append(partsParams, limitParam)
	partsPresent := append(append(append([]any(nil), sharePresent...), ownerPresent), append(rbMembershipTextBindings(22), rbMembershipTitleBindings(1)...)...)
	partsPresent = append(partsPresent, int64(rbMembershipFactLimit+1))
	partsEmpty := append(append(append([]any(nil), shareEmpty...), ownerEmpty), append(rbMembershipTextBindings(22), rbMembershipEmptyTitles()...)...)
	partsEmpty = append(partsEmpty, int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("saved.parts",
		"m.item_id,r.id", "m.item_id,octet_length(r.id)", partsFrom, partsWhere,
		partsParams, partsPresent, partsEmpty,
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}, PrimaryKeys: []string{"m"}, Seeks: []string{"resource_id=?", "entity_id=?"}})...)

	// Playlists: the viewer's own or shared playlists' entries, lifted through
	// the membership item index to this page's titles.
	listFrom := "catalog_playlists AS l CROSS JOIN catalog_playlist_entries AS e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=l.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=e.item_id CROSS JOIN catalog_entities AS me ON me.id=e.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id LEFT JOIN playlist_shares AS sh ON sh.playlist_id=l.token AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?"
	listWhere := "l.token IN(" + rbMembershipValues() + ") AND l.deleted=0 AND e.item_id IS NOT NULL AND (l.owner_authority=? AND l.owner_account=? AND l.owner_profile=? OR sh.playlist_id IS NOT NULL) AND m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	listParams := append(append(append(append([]stmt.Param(nil), shareParams...), s("owner_authority"), s("owner_account"), s("owner_profile")), append(rbMembershipTextParams(), idParams...)...), admission...)
	listParams = append(listParams, limitParam)
	listPresent := append(append(append(append([]any(nil), sharePresent...), "local", "a", "p"), append(rbMembershipTextBindings(23), rbMembershipTitleBindings(1)...)...), admissionPresent...)
	listPresent = append(listPresent, int64(rbMembershipFactLimit+1))
	listEmpty := append(append(append(append([]any(nil), shareEmpty...), "absent", "absent", "absent"), append(rbMembershipTextBindings(23), rbMembershipEmptyTitles()...)...), admissionEmpty...)
	listEmpty = append(listEmpty, int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("playlist",
		"m.entity_id,l.token", "m.entity_id,octet_length(l.token)", listFrom, listWhere,
		listParams, listPresent, listEmpty,
		stmt.Plan{Hot: true, Indexes: []string{"catalog_playlist_entries_page", "catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"playlist_id=?", "item_id=?"}})...)

	// A song's album title and its credited artist titles through the real
	// membership shape (a container's members are its songs).
	albumFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_songs AS sng ON sng.entity_id=m.item_id CROSS JOIN catalog_entities AS alb ON alb.id=sng.album_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	albumWhere := "m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	albumParams := append(append(append([]stmt.Param(nil), idParams...), admission...), limitParam)
	albumPresent := append(append(append([]any(nil), idsPresent...), admissionPresent...), int64(rbMembershipFactLimit+1))
	albumEmpty := append(append(append([]any(nil), idsEmpty...), admissionEmpty...), int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("album",
		"m.entity_id,alb.title", "m.entity_id,octet_length(alb.title)", albumFrom, albumWhere,
		albumParams, albumPresent, albumEmpty,
		stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "me", "ml"}})...)

	artistFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_song_artists AS sar ON sar.song_id=m.item_id CROSS JOIN catalog_entities AS art ON art.id=sar.artist_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	artistWhere := "m.entity_id IN(" + rbMembershipIDs() + ") AND" + rbMembershipAdmissionSQL
	artistParams := append(append(append([]stmt.Param(nil), idParams...), admission...), limitParam)
	artistPresent := append(append(append([]any(nil), idsPresent...), admissionPresent...), int64(rbMembershipFactLimit+1))
	artistEmpty := append(append(append([]any(nil), idsEmpty...), admissionEmpty...), int64(rbMembershipFactLimit+1))
	defs = append(defs, rbMembershipFactShape("artist",
		"m.entity_id,art.title", "m.entity_id,octet_length(art.title)", artistFrom, artistWhere,
		artistParams, artistPresent, artistEmpty,
		stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "sar", "me", "ml"}})...)

	// Episode groups. all/specials/unassigned_absolute are read from the
	// canonical show/season hierarchy; the range group needs the show's
	// authoritative rank source and refuses in the reader.
	episodeWhere := "e.id IN(" + rbMembershipIDs() + ") AND e.kind=4 AND e.show_id=show.id"
	episodeParams := append([]stmt.Param{b("show")}, append(idParams, limitParam)...)
	episodePresent := append([]any{rbMembershipPublicBinding(7)}, append(rbMembershipTitleBindings(1), int64(rbMembershipFactLimit+1))...)
	episodeEmpty := append([]any{rbMembershipPublicBinding(7)}, append(rbMembershipEmptyTitles(), int64(rbMembershipFactLimit+1))...)
	addEpisode := func(name, from, where string, plan stmt.Plan) {
		defs = append(defs, stmt.Definition{ID: readStatementID("rb.filter_membership.episode." + name), SQL: "SELECT e.id FROM " + from + " WHERE " + where + " LIMIT ?", Owner: "store/read", Kind: stmt.Read, CallSites: []string{rbMembershipCall}, Params: episodeParams, Cohorts: []stmt.Cohort{{Name: "present", Bindings: episodePresent}, {Name: "empty", Bindings: episodeEmpty}}, Plan: plan})
	}
	addEpisode("all", "catalog_entities AS e CROSS JOIN catalog_entities AS show ON show.public_id=?", episodeWhere, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	addEpisode("specials", "catalog_entities AS e CROSS JOIN catalog_entities AS show ON show.public_id=? CROSS JOIN catalog_entities AS season ON season.id=e.season_id", episodeWhere+" AND season.kind=3 AND season.season_number=0", stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "season"}})
	addEpisode("unassigned", "catalog_entities AS e CROSS JOIN catalog_entities AS show ON show.public_id=?", episodeWhere+" AND e.season_id IS NULL AND e.numbering='absolute'", stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	return defs
}

func rbMembershipTitleBindings(seed int64) []any {
	out := make([]any, rbMembershipSlot)
	for n := range out {
		out[n] = seed + int64(n) + 1
	}
	return out
}

func rbMembershipEmptyTitles() []any {
	out := make([]any, rbMembershipSlot)
	for n := range out {
		out[n] = int64(-1)
	}
	return out
}

func rbMembershipBlobBindings(seed byte) []any {
	out := make([]any, rbMembershipValueSlot)
	for n := range out {
		out[n] = []byte{seed, byte(n)}
	}
	return out
}

func rbMembershipTextBindings(seed int) []any {
	out := make([]any, rbMembershipValueSlot)
	for n := range out {
		out[n] = "value-" + strconv.Itoa(seed+n)
	}
	return out
}

func rbMembershipPublicBinding(id int64) []byte {
	out := make([]byte, 16)
	out[15] = byte(id)
	return out
}
