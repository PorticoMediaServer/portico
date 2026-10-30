package catalog

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Groups of titles the common browse route reads beyond a library's own
// containers: a personal collection, one of a show's episode groups and a Home
// row, and the group's own order (groupOrder) that survives a filter.
//
// Every group here is read from its own members (a collection's entries, a
// show's episodes, a row's titles), so its cost follows the group's size and
// not the library's.

// browseNodeValues is a predicate's operand as a list.
func browseNodeValues(node BrowseNode) []any {
	if list, ok := node.Value.([]any); ok {
		return list
	}
	return []any{node.Value}
}

// profileParts splits a qualified personal key into its authority, account and
// profile, as list and collection shares are stored.
func profileParts(profile string) ([]string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(profile, "viewer:"))
	var owner []string
	if err != nil || !strings.HasPrefix(profile, "viewer:") || json.Unmarshal(raw, &owner) != nil || len(owner) != 3 {
		return nil, false
	}
	return owner, true
}

// savedCollectionPredicate matches a personal collection's titles: an entry
// itself, the title that holds an entry (a show for a saved episode, checked
// against the viewer's restriction on that episode), and the parts of an entry
// that holds others (a saved show's episodes). The collection must be the
// viewer's, shared with them, or shared with everyone.
func (c *browseCompiler) savedCollectionPredicate(node BrowseNode) (string, error) {
	negative := node.Operator == "not-in"
	owner, ok := profileParts(c.profile)
	if !ok {
		if negative {
			return "1", nil
		}
		return "0", nil
	}
	values := browseNodeValues(node)
	entries := func() string {
		c.args = append(c.args, values...)
		c.args = append(c.args, c.profile, owner[0], owner[1], owner[2])
		return `SELECT sre.item_id FROM saved_resources res JOIN saved_resource_entries sre ON sre.resource_id=res.id WHERE res.id IN(` + placeholders(len(values)) + `) AND res.kind='collection' AND res.deleted=0 AND sre.item_id IS NOT NULL
 AND (res.owner_key=? OR res.visibility='server' OR EXISTS(SELECT 1 FROM saved_resource_shares share WHERE share.resource_id=res.id AND share.authority=? AND share.account_id=? AND share.profile_id=?))`
	}
	if c.bounded {
		direct := `EXISTS(SELECT 1 FROM (` + entries() + ` AND sre.item_id=e.entity_id))`
		held := `EXISTS(SELECT 1 FROM catalog_browse_memberships bei WHERE bei.entity_id=e.entity_id AND EXISTS(SELECT 1 FROM (` + entries() + ` AND sre.item_id=bei.item_id)) AND `
		restriction, args := ItemRestrictionSQL("bei.item_id", c.restrictions)
		c.args = append(c.args, args...)
		held += restriction + `)`
		parts := `EXISTS(SELECT 1 FROM catalog_browse_memberships bei WHERE bei.item_id=e.entity_id AND EXISTS(SELECT 1 FROM (` + entries() + ` AND sre.item_id=bei.entity_id)))`
		clause := "(" + direct + " OR " + held + " OR " + parts + ")"
		if negative {
			clause = "NOT " + clause
		}
		return clause, nil
	}
	direct := `e.entity_id IN(` + entries() + `)`
	held := `e.entity_id IN(SELECT bei.entity_id FROM (` + entries() + `) entry CROSS JOIN catalog_browse_memberships bei INDEXED BY catalog_browse_membership_item ON bei.item_id=entry.item_id WHERE `
	restriction, args := ItemRestrictionSQL("bei.item_id", c.restrictions)
	c.args = append(c.args, args...)
	held += restriction + `)`
	parts := `e.entity_id IN(SELECT bei.item_id FROM (` + entries() + `) entry CROSS JOIN catalog_browse_memberships bei ON bei.entity_id=entry.item_id)`
	clause := "(" + direct + " OR " + held + " OR " + parts + ")"
	if negative {
		return "NOT " + clause, nil
	}
	return clause, nil
}

// splitEpisodeGroup reads "<show id>/<group>", the group as the show
// workspace names it.
func splitEpisodeGroup(value any) (show, group string, ok bool) {
	text, _ := value.(string)
	at := strings.LastIndex(text, "/")
	if at <= 0 {
		return "", "", false
	}
	show, group = text[:at], text[at+1:]
	return show, group, validShowGroup(group)
}

// episodeGroupPredicate matches the episodes of one of a show's groups: its
// whole list, its specials, its episodes numbered only absolutely, or a range
// of a hundred regular episodes in the show's own order as this viewer sees it
// (the same ranking the show page draws its ranges from).
func (c *browseCompiler) episodeGroupPredicate(node BrowseNode) (string, error) {
	show, group, ok := splitEpisodeGroup(node.Value)
	if !ok {
		return "", browseIssue("query.value", "an episode group is a show id and one of its groups")
	}
	c.args = append(c.args, show)
	of := `ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`
	if c.bounded && !strings.HasPrefix(group, "range:") {
		of += ` AND ep.entity_id=e.entity_id`
	}
	switch {
	case group == "all":
		return `e.entity_id IN(SELECT ep.entity_id FROM catalog_episodes ep WHERE ` + of + `)`, nil
	case group == "specials":
		return `e.entity_id IN(SELECT ep.entity_id FROM catalog_episodes ep JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ` + of + ` AND sd.number=0)`, nil
	case group == "unassigned_absolute":
		return `e.entity_id IN(SELECT ep.entity_id FROM catalog_episodes ep WHERE ` + of + ` AND ep.season_id IS NULL AND ep.numbering='absolute')`, nil
	}
	start, _ := strconv.Atoi(strings.TrimPrefix(group, "range:"))
	restriction, args := ItemRestrictionSQL("ep.entity_id", c.restrictions)
	c.args = append(c.args, args...)
	c.args = append(c.args, start, start+showRangeSize)
	return `e.entity_id IN(SELECT ranked.id FROM (SELECT ep.entity_id AS id,ROW_NUMBER() OVER (ORDER BY COALESCE(sd.number,1),ep.number,ep.entity_id) AS n
 FROM catalog_episodes ep LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id
 WHERE ` + of + ` AND COALESCE(sd.number,1)<>0 AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=ep.entity_id) AND ` + restriction + `) ranked WHERE ranked.n>? AND ranked.n<=?)`, nil
}

// ErrBrowseHomeRow is returned for a Home row the request could not resolve.
var ErrBrowseHomeRow = browseIssue("query.value", "that Home row is not available to this viewer")

// homeRowPredicate matches the titles a Home row holds now. The caller resolved
// the row through Home itself (ResolveHomeRows), so it is the viewer's row.
func (c *browseCompiler) homeRowPredicate(node BrowseNode) (string, error) {
	id, _ := node.Value.(string)
	if source, ok := c.rowSources[id]; ok {
		c.args = append(c.args, source.args...)
		return `e.entity_id IN(SELECT id FROM (` + source.query + `))`, nil
	}
	ids, ok := c.rows[id]
	if !ok {
		return "", ErrBrowseHomeRow
	}
	if len(ids) == 0 {
		return "0", nil
	}
	raw, _ := json.Marshal(ids)
	c.args = append(c.args, string(raw))
	return `e.entity_id IN(SELECT value FROM json_each(?))`, nil
}

// BrowseHomeRows lists the Home rows a query names, so the caller can resolve
// them through Home before the browse.
func BrowseHomeRows(node *BrowseNode) []string {
	out := []string{}
	var walk func(*BrowseNode)
	walk = func(n *BrowseNode) {
		if n == nil {
			return
		}
		if n.Field == "homeRow" {
			if id, ok := n.Value.(string); ok && !containsString(out, id) {
				out = append(out, id)
			}
		}
		for i := range n.All {
			walk(&n.All[i])
		}
		for i := range n.Any {
			walk(&n.Any[i])
		}
		walk(n.Not)
	}
	walk(node)
	return out
}

// HomeRowMembers reads a Home row's titles in the row's order, as its See all
// page pages them. Queue capture only; browse uses ResolveHomeRowMembership.
func (s *Service) HomeRowMembers(r HomeRequest, id string) ([]int64, error) {
	out, _, err := s.HomeRowSnapshot(r, id)
	return out, err
}

// HomeRowSnapshot is HomeRowMembers with the row's title, for a queue that
// plays what the row shows now (its members at the moment Play is pressed).
func (s *Service) HomeRowSnapshot(r HomeRequest, id string) ([]int64, string, error) {
	out := []int64{}
	public := []string{}
	cursor, title := "", ""
	revision := ""
	for {
		row, err := s.HomeSingleRow(r, id, HomeRowPage{Cursor: cursor, Revision: revision, Limit: HomeRowMaxLimit})
		if err != nil {
			return nil, "", err
		}
		if title == "" {
			revision = fmt.Sprintf("%d:%d", row.Revision.Catalog, row.Revision.Viewer)
			title = row.Title
		}
		for _, entry := range row.Entries {
			public = append(public, entry.ID)
		}
		if row.NextCursor == "" {
			break
		}
		cursor = row.NextCursor
	}

	if len(public) == 0 {
		return out, title, nil
	}
	rows, err := s.read().Query(`SELECT j.key,ce.id FROM json_each(?) j JOIN catalog_entities ce ON ce.public_id=pid_blob(j.value) ORDER BY j.key`, idsJSON(public))
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var at int
		var entity int64
		if err = rows.Scan(&at, &entity); err != nil {
			return nil, "", err
		}
		out = append(out, entity)
	}
	return out, title, rows.Err()
}

// ---- the group's own order ----

// groupOrderTarget is the group condition a query's own order follows: the
// query itself or one condition of its top-level all, naming one collection,
// playlist, container, episode group or Home row.
func groupOrderTarget(node *BrowseNode) *BrowseNode {
	if node == nil {
		return nil
	}
	ordered := func(n *BrowseNode) bool {
		if _, ok := n.Value.(string); !ok || n.Operator != "equals" {
			return false
		}
		switch n.Field {
		case "collection", "list", "memberOf", "episodeGroup", "homeRow":
			return true
		}
		return false
	}
	if node.Field != "" {
		if ordered(node) {
			return node
		}
		return nil
	}
	for i := range node.All {
		if node.All[i].Field != "" && ordered(&node.All[i]) {
			return &node.All[i]
		}
	}
	return nil
}

// ErrBrowseGroupOrder refuses the group's own order where the query names no
// group that has one.
var ErrBrowseGroupOrder = browseIssue("sort[0].field", "groupOrder needs a collection, playlist, container, episode group or Home row condition")

// groupOrderSource is SQL yielding (id, pos): every browse entity of the
// group's members with its place in the group's own order. Members are put in
// order as playable items and mapped to the titles that hold them, a title
// taking its first member's place; so a show's place in a collection is its
// first collected episode's, and an album's place on an artist's page its
// first song's.
func (s *Service) groupOrderSource(target *BrowseNode, rows map[string][]int64, sources ...map[string]HomeRowMembership) (string, []any, error) {
	if target == nil {
		return "", nil, ErrBrowseGroupOrder
	}
	value, _ := target.Value.(string)
	up := func(items string) string {
		return `SELECT bm.entity_id AS id,min(o.pos) AS pos FROM (` + items + `) o CROSS JOIN catalog_browse_memberships bm INDEXED BY catalog_browse_membership_item ON bm.item_id=o.id GROUP BY bm.entity_id`
	}
	switch target.Field {
	case "collection":
		return up(`SELECT cm.item_id AS id,ROW_NUMBER() OVER (ORDER BY cm.order_key,cm.item_id) AS pos FROM catalog_collection_members cm WHERE cm.collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`), []any{value}, nil
	case "list":
		return up(`SELECT entry.item_id AS id,ROW_NUMBER() OVER (ORDER BY entry.order_key,entry.id) AS pos FROM catalog_playlists list JOIN catalog_playlist_entries entry ON entry.playlist_id=list.id WHERE list.token=? AND list.deleted=0 AND entry.item_id IS NOT NULL`), []any{value}, nil
	case "homeRow":
		if len(sources) > 0 {
			if source, ok := sources[0][value]; ok {
				return source.query, source.args, nil
			}
		}
		ids, ok := rows[value]
		if !ok {
			return "", nil, ErrBrowseHomeRow
		}
		raw, _ := json.Marshal(ids)
		// A row lists titles: each is its own browse entity, in the row's order.
		return `SELECT value AS id,min(key) AS pos FROM json_each(?) GROUP BY value`, []any{string(raw)}, nil
	case "episodeGroup":
		show, _, ok := splitEpisodeGroup(value)
		if !ok {
			return "", nil, browseIssue("query.value", "an episode group is a show id and one of its groups")
		}
		return up(showEpisodeOrder), []any{show}, nil
	}
	var kind int
	err := s.read().QueryRow(`SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, value).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return `SELECT 0 AS id,0 AS pos WHERE 0`, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	switch compactcatalog.Kind(kind) {
	case compactcatalog.Show:
		return up(showEpisodeOrder), []any{value}, nil
	case compactcatalog.Season:
		return up(`SELECT ep.entity_id AS id,ROW_NUMBER() OVER (ORDER BY ep.number,ep.entity_id) AS pos FROM catalog_episodes ep WHERE ep.season_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`), []any{value}, nil
	case compactcatalog.Album:
		return up(`SELECT s.entity_id AS id,ROW_NUMBER() OVER (ORDER BY COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id) AS pos FROM catalog_songs s WHERE s.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`), []any{value}, nil
	case compactcatalog.Book:
		return up(`SELECT f.entity_id AS id,ROW_NUMBER() OVER (ORDER BY COALESCE(f.disc_number,1),COALESCE(f.part_number,0),f.entity_id) AS pos FROM catalog_book_files f WHERE f.book_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`), []any{value}, nil
	case compactcatalog.Artist:
		// The artist page's releases: their own albums by type (albums, EPs,
		// singles, compilations), then those they appear on; newest first
		// within each, an unknown year last. Songs follow their album.
		return up(`SELECT s.entity_id AS id,ROW_NUMBER() OVER (ORDER BY releases.role,CASE WHEN a.year=0 THEN 1 ELSE 0 END,a.year DESC,a.sort_key COLLATE NOCASE,a.id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id) AS pos
 FROM (SELECT albums.id,CASE WHEN details.artist_id<>artist.id THEN 4
  WHEN EXISTS(SELECT 1 FROM mb_album_links l JOIN mb_release_group_types t ON t.release_revision=l.release_revision WHERE l.album_id=albums.id AND t.value='Compilation' COLLATE NOCASE) THEN 3
  WHEN COALESCE((SELECT e.primary_type FROM mb_album_links l JOIN mb_release_evidence e ON e.revision_id=l.release_revision WHERE l.album_id=albums.id),'Album')='EP' THEN 1
  WHEN COALESCE((SELECT e.primary_type FROM mb_album_links l JOIN mb_release_evidence e ON e.revision_id=l.release_revision WHERE l.album_id=albums.id),'Album')='Single' THEN 2
  ELSE 0 END AS role
  FROM (SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) artist
  CROSS JOIN (SELECT aa.entity_id AS id FROM catalog_albums aa INDEXED BY catalog_albums_artist WHERE aa.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))
   UNION SELECT ss.album_id FROM catalog_song_artists sa INDEXED BY catalog_song_artists_artist CROSS JOIN catalog_songs ss ON ss.entity_id=sa.song_id WHERE sa.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))) albums
  JOIN catalog_albums details ON details.entity_id=albums.id) releases
 JOIN catalog_entities a ON a.id=releases.id JOIN catalog_songs s ON s.album_id=releases.id`), []any{value, value, value}, nil
	case compactcatalog.Author:
		// An author's books in series order (a series, then its numbered place),
		// standalone books after them by title; parts follow their book.
		return up(`SELECT f.entity_id AS id,ROW_NUMBER() OVER (ORDER BY bk.series='',bk.series COLLATE NOCASE,CAST(NULLIF(bk.series_position,'') AS REAL),b.sort_key COLLATE NOCASE,b.id,COALESCE(f.disc_number,1),COALESCE(f.part_number,0),f.entity_id) AS pos
 FROM (SELECT DISTINCT bf.book_id FROM catalog_browse_memberships m CROSS JOIN catalog_book_files bf ON bf.entity_id=m.item_id WHERE m.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))) author
 JOIN catalog_books bk ON bk.entity_id=author.book_id JOIN catalog_entities b ON b.id=author.book_id JOIN catalog_book_files f ON f.book_id=author.book_id`), []any{value}, nil
	}
	return "", nil, ErrBrowseGroupOrder
}

// showEpisodeOrder is a show's episodes in the show page's order: seasons in
// number, specials last, then episode number.
const showEpisodeOrder = `SELECT ep.entity_id AS id,ROW_NUMBER() OVER (ORDER BY (COALESCE(sd.number,1)=0),COALESCE(sd.number,1),ep.number,ep.entity_id) AS pos
 FROM catalog_episodes ep LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`

// groupOrderPage reads a page of the group in its own order: the group's
// members (bounded by the group) joined to the filtered rows. A continuation
// seeks the last place it read; a direct range skips within the group.
func (s *Service) groupOrderPage(p pageShape) ([]browseRow, error) {
	_, desc := p.primary()
	direction, op := "ASC", ">"
	if desc {
		direction, op = "DESC", "<"
	}
	args := append(append([]any{}, p.groupArgs...), p.args...)
	where := p.where
	offset := p.start
	if len(p.seekKeys) > 0 {
		at, err := strconv.ParseInt(p.seekKeys[0], 10, 64)
		if err != nil {
			return nil, ErrCursor
		}
		where += ` AND ord.pos` + op + `?`
		args = append(args, at)
		offset = 0
	}
	args = append(args, p.limit, offset)
	return s.queryBrowseKeyRows(`WITH ord(id,pos) AS MATERIALIZED (`+p.group+`)
 SELECT `+browseRowColumns+`,ord.pos FROM ord CROSS JOIN catalog_browse_rows e ON e.entity_id=ord.id CROSS JOIN catalog_entities ce ON ce.id=e.entity_id
 WHERE `+where+` ORDER BY ord.pos `+direction+`,e.entity_id `+direction+` LIMIT ? OFFSET ?`, "groupOrder", args...)
}

// browseGroupFence is what a query's groups add to its revision: a playlist's
// and a personal collection's own revisions (their entries are not catalogue
// facts), and a Home row's current titles.
func (s *Service) browseGroupFence(node *BrowseNode, rows map[string][]int64, sources ...map[string]HomeRowMembership) (string, error) {
	lists, saved := []string{}, []string{}
	var walk func(*BrowseNode)
	walk = func(n *BrowseNode) {
		if n == nil {
			return
		}
		switch n.Field {
		case "list", "savedCollection":
			for _, v := range browseNodeValues(*n) {
				if text, ok := v.(string); ok {
					if n.Field == "list" {
						lists = append(lists, text)
					} else {
						saved = append(saved, text)
					}
				}
			}
		}
		for i := range n.All {
			walk(&n.All[i])
		}
		for i := range n.Any {
			walk(&n.Any[i])
		}
		walk(n.Not)
	}
	walk(node)
	parts := []string{}
	if len(lists) > 0 {
		var fence sql.NullString
		if err := s.read().QueryRow(`SELECT group_concat(token||':'||revision||':'||entry_count||':'||deleted) FROM catalog_playlists WHERE token IN(SELECT value FROM json_each(?))`, idsJSON(lists)).Scan(&fence); err != nil {
			return "", err
		}
		parts = append(parts, "lists="+fence.String)
	}
	if len(saved) > 0 {
		var fence sql.NullString
		if err := s.read().QueryRow(`SELECT group_concat(id||':'||revision||':'||deleted||':'||visibility) FROM saved_resources WHERE id IN(SELECT value FROM json_each(?))`, idsJSON(saved)).Scan(&fence); err != nil {
			return "", err
		}
		// saved_resource_generation moves on every entry INSERT/UPDATE/DELETE as
		// well as resource/share changes. Counting all collection entries adds no
		// fence strength and would turn a preview page into an unbounded scan.
		var shares int64
		if err := s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM saved_resource_generation WHERE id=1),0)`).Scan(&shares); err != nil {
			return "", err
		}
		parts = append(parts, "saved="+fence.String+":"+strconv.FormatInt(shares, 10))
	}
	for _, id := range BrowseHomeRows(node) {
		if len(sources) > 0 {
			if source, ok := sources[0][id]; ok {
				parts = append(parts, "row="+id+":"+source.fence)
				continue
			}
		}
		raw, _ := json.Marshal(rows[id])
		parts = append(parts, "row="+id+":"+string(raw))
	}
	return strings.Join(parts, "\x00"), nil
}
