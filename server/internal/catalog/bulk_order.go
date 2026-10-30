package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Encode SQLite scalar ordering using only the playlist key alphabet. Text has
// an explicit terminator, so descending prefixes sort correctly too.
func bulkOrderValue(value any, desc bool) string {
	out := "1"
	switch v := value.(type) {
	case int64:
		out = bulkOrderValue(float64(v), false)
	case float64:
		bits := math.Float64bits(v)
		if bits>>63 != 0 {
			bits = ^bits
		} else {
			bits ^= 1 << 63
		}
		out = fmt.Sprintf("2%016X", bits)
	case []byte:
		out = bulkOrderValue(string(v), false)
	case string:
		var b strings.Builder
		b.WriteByte('3')
		for _, c := range []byte(v) {
			fmt.Fprintf(&b, "%03X", int(c)+1)
		}
		b.WriteString("000")
		out = b.String()
	}
	if desc {
		raw := []byte(out)
		for i, c := range raw {
			raw[i] = playlistDigits[len(playlistDigits)-1-strings.IndexByte(playlistDigits, c)]
		}
		out = string(raw)
	}
	return out
}
func asciiFold(s string) string {
	raw := []byte(s)
	for i, c := range raw {
		if c >= 'A' && c <= 'Z' {
			raw[i] = c + 32
		}
	}
	return string(raw)
}
func scalarKeys(ctx context.Context, tx *sql.Tx, query string, args []any, desc, fold []bool) (string, error) {
	values := make([]any, len(desc))
	dest := make([]any, len(desc))
	for i := range dest {
		dest[i] = &values[i]
	}
	if e := tx.QueryRowContext(ctx, query, args...).Scan(dest...); e != nil {
		return "", e
	}
	key := ""
	for i, v := range values {
		if fold != nil && fold[i] {
			if text, ok := v.(string); ok {
				v = asciiFold(text)
			}
		}
		key += bulkOrderValue(v, desc[i])
	}
	return key, nil
}
func (s *Service) bulkSortKey(ctx context.Context, tx *sql.Tx, v Viewer, selector JobSelector, item int64) (string, error) {
	return s.bulkSortKeyReserved(ctx, tx, v, selector, item, nil)
}

func (s *Service) bulkSortKeyReserved(ctx context.Context, tx *sql.Tx, v Viewer, selector JobSelector, item int64, reserve func(int64) error) (string, error) {
	tie := bulkOrderValue(item, false) + "V"
	if selector.Items != nil {
		var public string
		if e := tx.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, item).Scan(&public); e != nil {
			return "", e
		}
		for index, id := range selector.Items.IDs {
			if id == public {
				return fmt.Sprintf("%020d", index) + tie, nil
			}
		}
	}
	prefix := ""
	if q := selector.Query; q != nil {
		var node *BrowseNode
		if len(q.Filter) > 0 {
			if err := json.Unmarshal(q.Filter, &node); err != nil {
				return "", err
			}
		}
		localReplay := selectionBatchHasLocalSearch(node)
		var libraryKind string
		if e := tx.QueryRowContext(ctx, `SELECT CASE kind WHEN 1 THEN 'movie' WHEN 2 THEN 'tv' WHEN 3 THEN 'anime' WHEN 4 THEN 'music' WHEN 5 THEN 'audiobook' END FROM catalog_libraries WHERE library_id=? AND retired=0`, q.LibraryID).Scan(&libraryKind); e != nil {
			return "", e
		}
		pivot, ok := pivotForKind(libraryKind, q.Pivot)
		if !ok {
			return "", ErrBrowsePivot
		}
		sorts, e := resolveBrowseSorts(pivot, q.Sort)
		if e != nil {
			return "", e
		}
		expressions := []string{}
		args := []any{}
		desc := []bool{}
		fold := []bool{}
		for _, entry := range sorts {
			definition, _ := browseSortByID(entry.Field)
			expression := definition.Expression
			if localReplay {
				switch entry.Field {
				case "personalRating":
					expression = "COALESCE((SELECT p.rating FROM catalog_personal_sort_rows p WHERE p.profile_id=? AND p.entity_id=e.entity_id),0)"
				case "lastPlayed":
					expression = "COALESCE((SELECT p.last_played_at FROM catalog_personal_sort_rows p WHERE p.profile_id=? AND p.entity_id=e.entity_id),'')"
				case "groupOrder":
					return "", ErrSelectionBatchUnsupported
				}
			}
			expressions = append(expressions, expression)
			if definition.Profile {
				args = append(args, v.Profile)
			}
			desc = append(desc, entry.Direction == "desc")
			fold = append(fold, entry.Field == "title")
		}
		expressions = append(expressions, "item.id")
		desc = append(desc, false)
		fold = append(fold, false)
		scope, scoped := browseScopeSQL(q.LibraryID, pivot)
		groupJoin := ""
		if !localReplay && sorts[0].Field == "groupOrder" {
			source, sourceArgs, err := s.groupOrderSource(groupOrderTarget(node), nil)
			if err != nil {
				return "", err
			}
			groupJoin = ` JOIN (` + source + `) ord ON ord.id=e.entity_id`
			args = append(args, sourceArgs...)
		}
		args = append(args, item)
		args = append(args, scoped...)
		compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: v.Profile, restrictions: v.EffectiveRestrictions()}
		readingEligible, e := s.bindReadingBrowse(q.LibraryID, compiler)
		if e != nil {
			return "", e
		}
		scope += " AND " + readingEligible
		if localReplay {
			// Capture already proved this leaf against the original query and
			// complete witness. Read its exact command-order scalar by identity;
			// do not send that search back through FTS or a member postfilter.
			if !selectionLeafPivot(pivot) {
				return "", ErrSelectionBatchUnsupported
			}
			source := ` FROM catalog_entities item JOIN catalog_browse_rows e ON e.entity_id=item.id WHERE item.id=? AND ` + scope
			if reserve != nil {
				sizes := make([]string, len(expressions))
				for i, expression := range expressions {
					sizes[i] = `COALESCE(length(CAST((` + expression + `) AS BLOB)),0)`
				}
				if e = reserveBulkSortScalars(ctx, tx, `SELECT `+strings.Join(sizes, "+")+source, args, reserve); e != nil {
					return "", e
				}
			}
			prefix, e = scalarKeys(ctx, tx, `SELECT `+strings.Join(expressions, ",")+source, args, desc, fold)
			if e != nil {
				return "", e
			}
		} else {
			filter, e := compiler.compile(node)
			if e != nil {
				return "", e
			}
			args = append(args, compiler.args...)
			order, ordered := browseOrderSQL(v.Profile, sorts)
			args = append(args, ordered...)
			prefix, e = scalarKeys(ctx, tx, `SELECT `+strings.Join(expressions, ",")+` FROM catalog_entities item JOIN catalog_browse_memberships bm ON bm.item_id=item.id JOIN catalog_browse_rows e ON e.entity_id=bm.entity_id`+groupJoin+` WHERE item.id=? AND `+scope+` AND `+filter+` ORDER BY `+order+` LIMIT 1`, args, desc, fold)
			if e != nil {
				return "", e
			}
		}
	}
	if c := selector.Container; c != nil && (c.Kind == "playlist" || c.Kind == "collection") {
		var key string
		query := `SELECT entry.order_key FROM catalog_playlist_entries entry JOIN catalog_playlists playlist ON playlist.id=entry.playlist_id WHERE playlist.token=? AND playlist.deleted=0 AND entry.item_id=? ORDER BY entry.order_key LIMIT 1`
		if c.Kind == "collection" {
			query = `SELECT member.order_key FROM catalog_collection_members member JOIN catalog_entities collection ON collection.id=member.collection_id WHERE collection.public_id=pid_blob(?) AND collection.retired=0 AND member.item_id=? ORDER BY member.order_key LIMIT 1`
		}
		e := tx.QueryRowContext(ctx, query, c.ID, item).Scan(&key)
		if e != nil {
			return "", e
		}
		return bulkOrderValue(key, false) + tie, nil
	}
	var kind string
	if e := tx.QueryRowContext(ctx, `SELECT `+compactBrowseKindSQL("kind")+` FROM catalog_entities WHERE id=?`, item).Scan(&kind); e != nil {
		return "", e
	}
	query := ""
	desc := []bool{}
	fold := []bool(nil)
	switch kind {
	case "episode":
		query = `SELECT COALESCE(season.number,1),CASE WHEN episode.numbering='date' THEN COALESCE(episode.air_date,0) ELSE episode.number END FROM catalog_entities item JOIN catalog_episodes episode ON episode.entity_id=item.id LEFT JOIN catalog_seasons season ON season.entity_id=episode.season_id WHERE item.id=?`
		desc = []bool{false, false}
	case "song":
		query = `SELECT album.year,album.title,album.id,song.disc_number,song.track_number FROM catalog_entities item JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_entities album ON album.id=song.album_id WHERE item.id=?`
		desc = []bool{false, false, false, false, false}
	case "audiobook_file":
		query = `SELECT part.disc_number,part.part_number FROM catalog_entities item JOIN catalog_book_files part ON part.entity_id=item.id WHERE item.id=?`
		desc = []bool{false, false}
	default:
		query = `SELECT title FROM catalog_entities WHERE id=?`
		desc = []bool{false}
		fold = []bool{true}
	}
	if reserve != nil {
		// The child-order arm reads numeric scalars plus at most an album/movie
		// title. Probe its maintained source before Go loads or encodes text.
		sizeQuery := `SELECT 512 FROM catalog_entities WHERE id=?`
		if kind == "song" {
			sizeQuery = `SELECT COALESCE(length(CAST(album.title AS BLOB)),0)+512 FROM catalog_songs song JOIN catalog_entities album ON album.id=song.album_id WHERE song.entity_id=?`
		}
		if kind != "song" && kind != "episode" && kind != "audiobook_file" {
			sizeQuery = `SELECT COALESCE(length(CAST(title AS BLOB)),0)+512 FROM catalog_entities WHERE id=?`
		}
		if e := reserveBulkSortScalars(ctx, tx, sizeQuery, []any{item}, reserve); e != nil {
			return "", e
		}
	}
	key, e := scalarKeys(ctx, tx, query, []any{item}, desc, fold)
	return prefix + key + tie, e
}

// Covers SQL/Go copies, NOCASE folding, builder growth, encoded retained keys
// and their concatenations. A legal oversized title yields typed capacity,
// never truncation or an unreserved command buffer.
func reserveBulkSortScalars(ctx context.Context, tx *sql.Tx, query string, args []any, reserve func(int64) error) error {
	var bytes int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&bytes); err != nil {
		return err
	}
	if bytes < 0 || bytes > (math.MaxInt64-8192)/32 {
		return ErrSelectionBatchCapacity
	}
	return reserve(32*bytes + 8192)
}
