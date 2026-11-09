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
		{Name: "allow_unrated", Type: stmt.Integer},
		{Name: "maximum_age_lo", Type: stmt.Integer},
		{Name: "maximum_age_hi", Type: stmt.Integer},
	}
}

const rbMembershipOctetAdmissionSQL = ` me.retired=0 AND ml.retired=0 AND (CASE WHEN me.admission_own_age IS NULL THEN ? ELSE CASE WHEN ?<0 THEN 1 ELSE me.admission_own_age<=? END END)<>0`

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
	ownerTriple := []stmt.Param{s("owner_authority"), s("owner_account"), s("owner_profile")}

	collectionFrom := "catalog_entities AS c CROSS JOIN catalog_collection_members AS cm ON cm.collection_id=c.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=cm.item_id CROSS JOIN catalog_entities AS me ON me.id=cm.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	collectionWhere := "c.public_id IN(" + rbMembershipOctetValues() + ") AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("collection",
		"SELECT m.entity_id,octet_length(c.public_id) FROM "+collectionFrom+" WHERE "+collectionWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), blobParams...), itemParams...), admission...), limit),
		stmt.Plan{Hot: true, Indexes: []string{"catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH m USING INDEX catalog_browse_membership_item (", "SEARCH cm USING PRIMARY KEY (", "SEARCH me USING INTEGER PRIMARY KEY ("})

	presenceFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_collection_members AS cm INDEXED BY catalog_collection_members_item ON cm.item_id=m.item_id CROSS JOIN catalog_entities AS c ON c.id=cm.collection_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	presenceWhere := "m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("collection.presence",
		"SELECT m.entity_id,octet_length(c.public_id) FROM "+presenceFrom+" WHERE "+presenceWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), itemParams...), admission...), limit)),
		stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}, PrimaryKeys: []string{"c", "me", "ml"}, Seeks: []string{"item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "catalog_collection_members", "catalog_collection_members_item", "c", []string{"item_id", "collection_id"})
		},
		[]string{"SEARCH cm USING INDEX catalog_collection_members_item (", "SEARCH m USING PRIMARY KEY ("})

	parentFrom := "catalog_entities AS c CROSS JOIN catalog_browse_memberships AS p ON p.entity_id=c.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=p.item_id CROSS JOIN catalog_entities AS me ON me.id=p.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	parentWhere := "c.public_id IN(" + rbMembershipOctetValues() + ") AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("parent",
		"SELECT m.entity_id,octet_length(c.public_id) FROM "+parentFrom+" WHERE "+parentWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), blobParams...), itemParams...), admission...), limit),
		stmt.Plan{Hot: true, Indexes: []string{"catalog_browse_membership_item"}, PrimaryKeys: []string{"p", "me", "ml"}, Seeks: []string{"entity_id=?", "item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH p USING PRIMARY KEY (", "SEARCH m USING INDEX catalog_browse_membership_item ("})

	savedFrom := "saved_resources AS r CROSS JOIN saved_resource_entries AS sre INDEXED BY saved_resource_entries_order ON sre.resource_id=r.id"
	savedShare := "LEFT JOIN saved_resource_shares AS sh ON sh.resource_id=r.id AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?"
	savedWhere := "r.id IN(" + rbMembershipOctetValues() + ") AND r.kind='collection' AND r.deleted=0 AND (r.owner_key=? OR r.visibility='server' OR sh.resource_id IS NOT NULL)"
	rbMembershipOctetFact("saved.direct",
		"SELECT sre.item_id,octet_length(r.id) FROM "+savedFrom+" "+savedShare+" WHERE "+savedWhere+" AND sre.item_id IN("+rbMembershipOctetIDs()+") LIMIT ?",
		append(append(append(append(append(append([]stmt.Param(nil), shareParams...), ownerParam), textParams...), itemParams...), limit)),
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}, Seeks: []string{"resource_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "saved_resource_entries", "saved_resource_entries_order", "c", []string{"resource_id", "order_key", "id"})
		},
		[]string{"SEARCH sre USING INDEX saved_resource_entries_order ("})

	heldFrom := savedFrom + " CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=sre.item_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id " + savedShare
	heldWhere := savedWhere + " AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	heldParams := append(append(append(append([]stmt.Param(nil), shareParams...), ownerParam), append(append(textParams, itemParams...), admission...)...), limit)
	rbMembershipOctetFact("saved.held",
		"SELECT m.entity_id,octet_length(r.id) FROM "+heldFrom+" WHERE "+heldWhere+" LIMIT ?",
		heldParams,
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order", "catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"resource_id=?", "item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBMembershipIndex(ctx, c, "saved_resource_entries", "saved_resource_entries_order", "c", []string{"resource_id", "order_key", "id"}); err != nil {
				return err
			}
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH sre USING INDEX saved_resource_entries_order (", "SEARCH m USING INDEX catalog_browse_membership_item ("})

	partsFrom := savedFrom + " CROSS JOIN catalog_browse_memberships AS m ON m.entity_id=sre.item_id " + savedShare
	partsWhere := savedWhere + " AND m.item_id IN(" + rbMembershipOctetIDs() + ")"
	partsParams := append(append(append([]stmt.Param(nil), shareParams...), ownerParam), append(append(textParams, itemParams...))...)
	partsParams = append(partsParams, limit)
	rbMembershipOctetFact("saved.parts",
		"SELECT m.item_id,octet_length(r.id) FROM "+partsFrom+" WHERE "+partsWhere+" LIMIT ?",
		partsParams,
		stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}, PrimaryKeys: []string{"m"}, Seeks: []string{"resource_id=?", "entity_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipIndex(ctx, c, "saved_resource_entries", "saved_resource_entries_order", "c", []string{"resource_id", "order_key", "id"})
		},
		[]string{"SEARCH sre USING INDEX saved_resource_entries_order (", "SEARCH m USING PRIMARY KEY ("})

	listFrom := "catalog_playlists AS l CROSS JOIN catalog_playlist_entries AS e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=l.id CROSS JOIN catalog_browse_memberships AS m INDEXED BY catalog_browse_membership_item ON m.item_id=e.item_id CROSS JOIN catalog_entities AS me ON me.id=e.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id LEFT JOIN playlist_shares AS sh ON sh.playlist_id=l.token AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?"
	listWhere := "l.token IN(" + rbMembershipOctetValues() + ") AND l.deleted=0 AND e.item_id IS NOT NULL AND (l.owner_authority=? AND l.owner_account=? AND l.owner_profile=? OR sh.playlist_id IS NOT NULL) AND m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	listParams := append(append(append(append(append([]stmt.Param(nil), shareParams...), ownerTriple...), append(textParams, itemParams...)...), admission...), limit)
	rbMembershipOctetFact("playlist",
		"SELECT m.entity_id,octet_length(l.token) FROM "+listFrom+" WHERE "+listWhere+" LIMIT ?",
		listParams,
		stmt.Plan{Hot: true, Indexes: []string{"catalog_playlist_entries_page", "catalog_browse_membership_item"}, PrimaryKeys: []string{"me", "ml"}, Seeks: []string{"playlist_id=?", "item_id=?"}},
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBMembershipIndex(ctx, c, "catalog_playlist_entries", "catalog_playlist_entries_page", "c", []string{"playlist_id", "order_key", "id"}); err != nil {
				return err
			}
			return auditRBMembershipIndex(ctx, c, "catalog_browse_memberships", "catalog_browse_membership_item", "c", []string{"item_id", "entity_id"})
		},
		[]string{"SEARCH e USING INDEX catalog_playlist_entries_page (", "SEARCH m USING INDEX catalog_browse_membership_item ("})

	albumFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_songs AS sng ON sng.entity_id=m.item_id CROSS JOIN catalog_entities AS alb ON alb.id=sng.album_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	albumWhere := "m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("album",
		"SELECT m.entity_id,octet_length(alb.title) FROM "+albumFrom+" WHERE "+albumWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), itemParams...), admission...), limit)),
		stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "me", "ml"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipTable(ctx, c, "catalog_browse_memberships", []string{"entity_id", "item_id", "source"})
		},
		[]string{"SEARCH m USING PRIMARY KEY ("})

	artistFrom := "catalog_browse_memberships AS m CROSS JOIN catalog_song_artists AS sar ON sar.song_id=m.item_id CROSS JOIN catalog_entities AS art ON art.id=sar.artist_id CROSS JOIN catalog_entities AS me ON me.id=m.item_id CROSS JOIN catalog_libraries AS ml ON ml.id=me.library_id"
	artistWhere := "m.entity_id IN(" + rbMembershipOctetIDs() + ") AND" + rbMembershipOctetAdmissionSQL
	rbMembershipOctetFact("artist",
		"SELECT m.entity_id,octet_length(art.title) FROM "+artistFrom+" WHERE "+artistWhere+" LIMIT ?",
		append(append(append(append([]stmt.Param(nil), itemParams...), admission...), limit)),
		stmt.Plan{Hot: true, PrimaryKeys: []string{"m", "sar", "me", "ml"}},
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBMembershipTable(ctx, c, "catalog_browse_memberships", []string{"entity_id", "item_id", "source"})
		},
		[]string{"SEARCH m USING PRIMARY KEY (", "SEARCH sar USING PRIMARY KEY ("})
}

func rbMembershipOctetFact(name, sqlText string, params []stmt.Param, proof stmt.Plan, audit func(context.Context, *sql.Conn) error, searches []string) {
	id := stmt.ID("read.browse.rb.filter_membership." + name + ".bytes")
	pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded membership fact byte-source range required",
		definition: func(d stmt.Definition) bool {
			return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
		},
		audit: audit,
		point: func(rows Rows) error { return rbMembershipOctetSearch(rows, searches) },
	})
}

func rbMembershipOctetSearch(rows Rows, wants []string) error {
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
			return fmt.Errorf("membership fact preflight must be SEARCH-only: %s", detail)
		}
		details = append(details, detail)
	}
	for _, want := range wants {
		found := false
		for _, detail := range details {
			if strings.Contains(detail, want) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("membership fact preflight plan changed: missing %q", want)
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
