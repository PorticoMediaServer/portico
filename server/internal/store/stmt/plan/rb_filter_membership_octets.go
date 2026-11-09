package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these compiled bounded title/fact ranges may preflight octets. This
// grants no arbitrary range, expression, identifier or SQL-function permission.
const (
	rbMembershipOctetSlot      = 64
	rbMembershipOctetValueSlot = 100
	rbMembershipOctetFactLimit = 2048
)

func rbMembershipOctetIDParams() []stmt.Param {
	out := make([]stmt.Param, rbMembershipOctetSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "title" + strconv.Itoa(i), Type: stmt.Integer}
	}
	return out
}
func rbMembershipOctetBlobParams() []stmt.Param {
	out := make([]stmt.Param, rbMembershipOctetValueSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "value" + strconv.Itoa(i), Type: stmt.Blob}
	}
	return out
}
func rbMembershipOctetTextParams() []stmt.Param {
	out := make([]stmt.Param, rbMembershipOctetValueSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "value" + strconv.Itoa(i), Type: stmt.Text}
	}
	return out
}
func rbMembershipOctetMarks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func rbMembershipOctetIDs() string        { return rbMembershipOctetMarks(rbMembershipOctetSlot) }
func rbMembershipOctetValues() string     { return rbMembershipOctetMarks(rbMembershipOctetValueSlot) }

func rbMembershipOctetAdmissionParams() []stmt.Param {
	return []stmt.Param{
		{Name: "null_age", Type: stmt.Integer, Nullable: true},
		{Name: "maximum_age", Type: stmt.Integer},
	}
}

// rbMembershipOctetAdmissionSQL is byte-identical to the payload admission
// expression in the read package: a NULL member age compares null_age, which is
// the ceiling when unrated items are allowed and NULL otherwise.
const rbMembershipOctetAdmissionSQL = ` me.retired=0 AND ml.retired=0 AND coalesce(me.admission_own_age,?)<=?`

func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }

	// Title source octets: one exact byte point per accepted article/direction
	// /part. The payload walk reuses the same native index and keyset seek.
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
			for _, part := range []string{"first", "tail", "next"} {
				where := " WHERE e.library_id=? AND e.kind=?"
				seek := "library_id=? AND kind=?"
				params := []stmt.Param{i("library"), i("kind")}
				if part == "tail" {
					where += " AND e." + key + " COLLATE NOCASE=? AND e.id" + op + "?"
					seek = key + "=? AND id" + op + "?"
					params = append(params, s("key"), i("after"))
				}
				if part == "next" {
					where += " AND e." + key + " COLLATE NOCASE" + op + "?"
					seek = key + op + "?"
					params = append(params, s("key"))
				}
				params = append(params, i("limit"))
				id := stmt.ID("read.browse.rb.filter_membership.entity." + variant + direction + part + ".bytes")
				sqlText := "SELECT e.id,octet_length(e." + key + "),octet_length(e.title) FROM catalog_entities AS e INDEXED BY " + index + where + " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
				proof := stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
				pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded membership title byte-source range required",
					definition: func(d stmt.Definition) bool {
						return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
					},
					audit: func(ctx context.Context, c *sql.Conn) error { return auditRBPersonalTitleIndex(ctx, c, index, key) },
					point: func(rows Rows) error {
						if len(rows.Values) != 1 || len(rows.Values[0]) != 4 {
							return fmt.Errorf("membership title byte source requires one indexed SEARCH")
						}
						text, ok := rows.Values[0][3].(string)
						if !ok || !strings.HasPrefix(text, "SEARCH e USING INDEX "+index+" (") {
							return fmt.Errorf("membership title byte source requires its exact indexed SEARCH")
						}
						return nil
					},
				})
			}
		}
	}

	// Fact byte points. Each registration owns the exact payload-adjacent byte
	// projection, its forced index or primary key path, and the reviewed SEARCH
	// lines. Unused slots bind -1 (or an empty text/blob), never a positive id.
	itemParams := rbMembershipOctetIDParams()
	blobParams := rbMembershipOctetBlobParams()
	textParams := rbMembershipOctetTextParams()
	admission := rbMembershipOctetAdmissionParams()
	limit := i("limit")
	shareParams := []stmt.Param{s("authority"), s("account"), s("profile")}
	ownerParam := s("owner_key")

	collectionFrom := "catalog_entities AS c CROSS JOIN catalog_collection_members AS cm ON cm.collection_id=c.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=cm.item_id CROSS JOIN catalog_entities AS me ON me.id=cm.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	collectionWhere := "c.public_id IN(" + rbMembershipOctetValues() + ") AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("collection",
		"SELECT m.entity_id,octet_length(c.public_id) FROM "+collectionFrom+" WHERE "+collectionWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), blobParams...), itemParams...), admission...), limit),
		stmt.Plan{Hot: true, Indexes: []string{"catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH m USING INDEX catalog_browse_membership_item (", "SEARCH cm USING INDEX catalog_collection_members_page (", "SEARCH me USING INTEGER PRIMARY KEY ("},
		"BLOOM FILTER ON me (id=?)", "BLOOM FILTER ON ml (id=?)", "BLOOM FILTER ON m (item_id=? AND entity_id=?)")

	presenceFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_collection_members AS cm INDEXED BY catalog_collection_members_item ON cm.item_id=m.item_id CROSS JOIN catalog_entities AS c ON c.id=cm.collection_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	presenceWhere := "m.entity_id IN(" + rbMembershipOctetIDs() + ") AND ml.id=? AND" + rbMembershipOctetAdmissionSQL
	presenceParams := append(append(append([]stmt.Param(nil), itemParams...), i("library")), append(admission, limit)...)
	rbMembershipOctetFact("collection.presence",
		"SELECT m.entity_id,octet_length(c.public_id) FROM "+presenceFrom+" WHERE "+presenceWhere+" LIMIT ?",
		presenceParams,
		stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}, PrimaryKeys: []string{"c", "me", "ml"}, Seeks: []string{"item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "catalog_collection_members", "catalog_collection_members_item", "c", []string{"item_id", "collection_id"})
		},
		[]string{"SEARCH cm USING INDEX catalog_collection_members_item (", "SEARCH m USING PRIMARY KEY ("},
		"BLOOM FILTER ON me (id=?)", "BLOOM FILTER ON ml (id=?)")

	parentFrom := "catalog_entities AS c CROSS JOIN catalog_browse_memberships AS p ON p.entity_id=c.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=p.item_id CROSS JOIN catalog_entities AS me ON me.id=p.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	parentWhere := "c.public_id IN(" + rbMembershipOctetValues() + ") AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("parent",
		"SELECT m.entity_id,octet_length(c.public_id) FROM "+parentFrom+" WHERE "+parentWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), blobParams...), itemParams...), admission...), limit),
		stmt.Plan{Hot: true, Indexes: []string{"catalog_browse_membership_item"}, PrimaryKeys: []string{"p", "me", "ml"}, Seeks: []string{"entity_id=?", "item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH p USING PRIMARY KEY (", "SEARCH m USING INDEX catalog_browse_membership_item ("},
		"BLOOM FILTER ON me (id=?)", "BLOOM FILTER ON ml (id=?)", "BLOOM FILTER ON m (item_id=? AND entity_id=?)")

	savedFrom := "saved_resource_entries AS sre INDEXED BY saved_resource_entries_order CROSS JOIN saved_resources AS r ON r.id=sre.resource_id"
	savedShare := "LEFT JOIN saved_resource_shares AS sh INDEXED BY saved_resource_shared ON sh.resource_id=r.id AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?"
	savedWhere := "sre.resource_id IN(" + rbMembershipOctetValues() + ") AND r.kind='collection' AND r.deleted=0 AND coalesce(nullif(length(r.owner_key),length(nullif(r.owner_key,?))),nullif(length(r.visibility),length(nullif(r.visibility,'server'))),length(sh.resource_id)) IS NOT NULL"
	rbMembershipOctetFact("saved.direct",
		"SELECT sre.item_id,octet_length(r.id) FROM "+savedFrom+" "+savedShare+" WHERE "+savedWhere+" AND sre.item_id IN("+rbMembershipOctetIDs()+") LIMIT ?",
		append(append(append(append(append([]stmt.Param(nil), shareParams...), textParams...), ownerParam), itemParams...), limit),
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order", "sqlite_autoindex_saved_resources_1", "saved_resource_shared"}, Seeks: []string{"resource_id=?", "id=?", "authority=? AND account_id=? AND profile_id=? AND resource_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "saved_resource_entries", "saved_resource_entries_order", "c", []string{"resource_id", "order_key", "id"})
		},
		[]string{"SEARCH sre USING INDEX saved_resource_entries_order ("},
		"BLOOM FILTER ON sh (authority=? AND account_id=? AND profile_id=? AND resource_id=?)")

	heldFrom := savedFrom + " CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=sre.item_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id " + savedShare
	heldWhere := savedWhere + " AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	heldParams := append(append(append([]stmt.Param(nil), shareParams...), textParams...), ownerParam)
	heldParams = append(heldParams, itemParams...)
	heldParams = append(heldParams, admission...)
	heldParams = append(heldParams, limit)
	rbMembershipOctetFact("saved.held",
		"SELECT m.entity_id,octet_length(r.id) FROM "+heldFrom+" WHERE "+heldWhere+" LIMIT ?",
		heldParams,
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order", "sqlite_autoindex_saved_resources_1", "catalog_browse_membership_item", "saved_resource_shared"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"resource_id=?", "id=?", "item_id=?", "authority=? AND account_id=? AND profile_id=? AND resource_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBMembershipIndex(ctx, c, "saved_resource_entries", "saved_resource_entries_order", "c", []string{"resource_id", "order_key", "id"}); err != nil {
				return err
			}
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH sre USING INDEX saved_resource_entries_order (", "SEARCH m USING INDEX catalog_browse_membership_item ("},
		"BLOOM FILTER ON me (id=?)", "BLOOM FILTER ON ml (id=?)", "BLOOM FILTER ON sh (authority=? AND account_id=? AND profile_id=? AND resource_id=?)")

	partsFrom := savedFrom + " CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.entity_id=sre.item_id " + savedShare
	partsWhere := savedWhere + " AND m.item_id IN(" + rbMembershipOctetIDs() + ")"
	partsParams := append(append(append([]stmt.Param(nil), shareParams...), textParams...), ownerParam)
	partsParams = append(partsParams, itemParams...)
	partsParams = append(partsParams, limit)
	rbMembershipOctetFact("saved.parts",
		"SELECT m.item_id,octet_length(r.id) FROM "+partsFrom+" WHERE "+partsWhere+" LIMIT ?",
		partsParams,
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order", "sqlite_autoindex_saved_resources_1", "catalog_browse_membership_item", "saved_resource_shared"}, Seeks: []string{"resource_id=?", "id=?", "entity_id=?", "authority=? AND account_id=? AND profile_id=? AND resource_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "saved_resource_entries", "saved_resource_entries_order", "c", []string{"resource_id", "order_key", "id"})
		},
		[]string{"SEARCH sre USING INDEX saved_resource_entries_order (", "SEARCH m USING INDEX catalog_browse_membership_item ("},
		"BLOOM FILTER ON sh (authority=? AND account_id=? AND profile_id=? AND resource_id=?)")

	listTail := " CROSS JOIN catalog_playlist_entries AS e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=l.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=e.item_id CROSS JOIN catalog_entities AS me ON me.id=e.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	listOwnerFrom := "catalog_playlists AS l INDEXED BY catalog_playlists_owner" + listTail
	listOwnerWhere := "l.owner_authority=? AND l.owner_account=? AND l.owner_profile=? AND l.deleted=0 AND l.token IN(" + rbMembershipOctetValues() + ") AND e.item_id IS NOT NULL AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	listOwnerParams := append(append(append([]stmt.Param(nil), shareParams...), textParams...), itemParams...)
	listOwnerParams = append(listOwnerParams, admission...)
	listOwnerParams = append(listOwnerParams, limit)
	rbMembershipOctetFact("playlist.owner",
		"SELECT m.entity_id,octet_length(l.token) FROM "+listOwnerFrom+" WHERE "+listOwnerWhere+" LIMIT ?",
		listOwnerParams,
		stmt.Plan{Hot: true, Indexes: []string{"catalog_playlists_owner", "catalog_playlist_entries_page", "catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"owner_authority=? AND owner_account=? AND owner_profile=? AND deleted=?", "playlist_id=?", "item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBMembershipTable(ctx, c, "catalog_playlists", []string{"id", "token", "owner_authority", "owner_account", "owner_profile", "deleted"}); err != nil {
				return err
			}
			if err := auditRBMembershipIndex(ctx, c, "catalog_playlist_entries", "catalog_playlist_entries_page", "c", []string{"playlist_id", "order_key", "id"}); err != nil {
				return err
			}
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH l USING INDEX catalog_playlists_owner (", "SEARCH e USING INDEX catalog_playlist_entries_page (", "SEARCH m USING INDEX catalog_browse_membership_item ("},
		"BLOOM FILTER ON e (playlist_id=?)", "BLOOM FILTER ON m (item_id=? AND entity_id=?)", "BLOOM FILTER ON me (id=?)", "BLOOM FILTER ON ml (id=?)")

	listSharedFrom := "playlist_shares AS sh INDEXED BY playlist_shares_listen_actor CROSS JOIN catalog_playlists AS l ON l.token=sh.playlist_id" + listTail
	listSharedWhere := "sh.authority=? AND sh.account_id=? AND sh.profile_id=? AND sh.playlist_id IN(" + rbMembershipOctetValues() + ") AND l.deleted=0 AND e.item_id IS NOT NULL AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	listSharedParams := append(append(append([]stmt.Param(nil), shareParams...), textParams...), itemParams...)
	listSharedParams = append(listSharedParams, admission...)
	listSharedParams = append(listSharedParams, limit)
	rbMembershipOctetFact("playlist.shared",
		"SELECT m.entity_id,octet_length(l.token) FROM "+listSharedFrom+" WHERE "+listSharedWhere+" LIMIT ?",
		listSharedParams,
		stmt.Plan{Hot: true, Indexes: []string{"playlist_shares_listen_actor", "sqlite_autoindex_catalog_playlists_1", "catalog_playlist_entries_page", "catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND playlist_id=?", "token=?", "playlist_id=?", "item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBMembershipIndex(ctx, c, "playlist_shares", "playlist_shares_listen_actor", "c", []string{"authority", "account_id", "profile_id", "playlist_id"}); err != nil {
				return err
			}
			if err := auditRBMembershipTable(ctx, c, "catalog_playlists", []string{"id", "token", "deleted"}); err != nil {
				return err
			}
			if err := auditRBMembershipIndex(ctx, c, "catalog_playlist_entries", "catalog_playlist_entries_page", "c", []string{"playlist_id", "order_key", "id"}); err != nil {
				return err
			}
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH sh USING INDEX playlist_shares_listen_actor (", "SEARCH l USING INDEX sqlite_autoindex_catalog_playlists_1 (", "SEARCH e USING INDEX catalog_playlist_entries_page (", "SEARCH m USING INDEX catalog_browse_membership_item ("},
		"BLOOM FILTER ON l (token=?)", "BLOOM FILTER ON sh (authority=? AND account_id=? AND profile_id=? AND playlist_id=?)", "BLOOM FILTER ON e (playlist_id=?)", "BLOOM FILTER ON m (item_id=? AND entity_id=?)", "BLOOM FILTER ON me (id=?)", "BLOOM FILTER ON ml (id=?)")

	albumFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_songs AS sng ON sng.entity_id=m.item_id CROSS JOIN catalog_entities AS alb ON alb.id=sng.album_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	albumWhere := "m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("album",
		"SELECT m.entity_id,octet_length(alb.title) FROM "+albumFrom+" WHERE "+albumWhere+" LIMIT ?",
		append(append(append([]stmt.Param(nil), itemParams...), admission...), limit),
		stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "me", "ml"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipTable(ctx, c, "catalog_browse_memberships", []string{"entity_id", "item_id", "source"})
		},
		[]string{"SEARCH m USING PRIMARY KEY ("},
		"BLOOM FILTER ON ml (id=?)")

	artistFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_song_artists AS sar ON sar.song_id=m.item_id CROSS JOIN catalog_entities AS art ON art.id=sar.artist_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	artistWhere := "m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("artist",
		"SELECT m.entity_id,octet_length(art.title) FROM "+artistFrom+" WHERE "+artistWhere+" LIMIT ?",
		append(append(append([]stmt.Param(nil), itemParams...), admission...), limit),
		stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "sar", "me", "ml"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipTable(ctx, c, "catalog_browse_memberships", []string{"entity_id", "item_id", "source"})
		},
		[]string{"SEARCH m USING PRIMARY KEY (", "SEARCH sar USING PRIMARY KEY ("},
		"BLOOM FILTER ON ml (id=?)")
}

// rbMembershipOctetScans are exact analyzed-plan table scans waived for a fact
// statement's bytes preflight. The saved and playlist statements force their
// resource/view identity and share primary keys, so every access is a keyed
// SEARCH in both analyzed profiles and no SCAN waiver is granted here.
var rbMembershipOctetScans = map[string][]string{}

func rbMembershipOctetFact(name, sqlText string, params []stmt.Param, proof stmt.Plan, audit func(context.Context, *sql.Conn) error, searches []string, bloom ...string) {
	id := stmt.ID("read.browse.rb.filter_membership." + name + ".bytes")
	proof.Waivers = nil
	for _, match := range rbMembershipOctetScans[name] {
		proof.Waivers = append(proof.Waivers, stmt.Waiver{StatementID: id, Stage: "eqp", Form: "SCAN", Match: match,
			Reason: "Analyzed estimate scans a small catalog dimension; the entry and membership seeks remain pinned"})
	}
	for _, match := range bloom {
		proof.Waivers = append(proof.Waivers, stmt.Waiver{StatementID: id, Stage: "eqp", Form: "BLOOM FILTER", Match: match,
			Reason: "Bundled planner join bloom filter; the exact indexed SEARCH lines are separately required"})
	}
	pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded membership fact byte-source range required",
		definition: func(d stmt.Definition) bool {
			return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
		},
		audit: audit,
		point: func(rows Rows) error { return rbMembershipOctetSearch(rows, searches, rbMembershipOctetScans[name]) },
	})
}

func rbMembershipOctetSearch(rows Rows, wants, allowedScans []string) error {
	details := make([]string, 0, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) != 4 {
			return fmt.Errorf("membership fact preflight invalid native EQP")
		}
		detail, ok := row[3].(string)
		if !ok {
			return fmt.Errorf("membership fact preflight invalid native EQP")
		}
		if strings.Contains(strings.ToUpper(detail), "SCAN ") {
			allowed := false
			for _, scan := range allowedScans {
				if detail == scan {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("membership fact preflight must be SEARCH-only: %s", detail)
			}
		}
		details = append(details, detail)
	}
	for _, want := range wants {
		found := false
		for _, detail := range details {
			// A covering SEARCH is the same reviewed access path with a
			// narrower projection; the index identity, origin and key order
			// are audited separately by auditRBMembershipIndex.
			if strings.Contains(strings.ReplaceAll(detail, "USING COVERING INDEX", "USING INDEX"), want) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("membership fact preflight plan changed: missing %q in %q", want, details)
		}
	}
	return nil
}

func auditRBMembershipIndex(ctx context.Context, c *sql.Conn, table, index, origin string, columns []string) error {
	list, err := query(ctx, c, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range list.Values {
		if len(row) != 5 {
			return fmt.Errorf("membership fact index metadata changed")
		}
		if row[1] != index {
			continue
		}
		if row[4] != int64(0) {
			return fmt.Errorf("membership fact index must be non-partial")
		}
		if origin != "" && row[3] != origin {
			return fmt.Errorf("membership fact index origin changed")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("membership fact native index absent")
	}
	if len(columns) == 0 {
		return nil
	}
	info, err := query(ctx, c, "PRAGMA main.index_xinfo("+identifier(index)+")")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("membership fact key metadata changed")
		}
		if row[5] != int64(1) {
			continue
		}
		if keys >= len(columns) || row[2] != columns[keys] || row[3] != int64(0) || row[4] != "BINARY" {
			return fmt.Errorf("membership fact index key changed")
		}
		keys++
	}
	if keys != len(columns) {
		return fmt.Errorf("membership fact index key incomplete")
	}
	return nil
}

func auditRBMembershipTable(ctx context.Context, c *sql.Conn, table string, columns []string) error {
	info, err := query(ctx, c, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for _, row := range info.Values {
		if len(row) != 7 {
			return fmt.Errorf("membership fact table metadata changed")
		}
		name, ok := row[1].(string)
		if !ok {
			return fmt.Errorf("membership fact table metadata changed")
		}
		found[name] = true
	}
	for _, column := range columns {
		if !found[column] {
			return fmt.Errorf("membership fact table key column absent")
		}
	}
	return nil
}
