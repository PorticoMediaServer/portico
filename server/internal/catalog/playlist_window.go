package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strconv"
	"strings"
)

const playlistDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Keys never end in zero, so a lexical midpoint always exists. No float
// rounding or full-playlist renumber is needed to insert between neighbors.
func playlistBetween(lower, upper string) string {
	prefix := ""
	for {
		lo, hi := 0, len(playlistDigits)
		if lower != "" {
			lo = strings.IndexByte(playlistDigits, lower[0])
		}
		if upper != "" {
			hi = strings.IndexByte(playlistDigits, upper[0])
		}
		if hi-lo > 1 {
			return prefix + string(playlistDigits[(lo+hi)/2])
		}
		prefix += string(playlistDigits[lo])
		if lower != "" {
			lower = lower[1:]
		}
		if hi == lo {
			upper = upper[1:]
		} else {
			upper = ""
		}
	}
}

// Divide the interval evenly rather than repeatedly approaching its upper
// edge. Large jobs therefore need logarithmic key depth, not depth per item.
func playlistRank(lower, upper string, index, count int64) string {
	mid := count / 2
	key := playlistBetween(lower, upper)
	if index == mid {
		return key
	}
	if index < mid {
		return playlistRank(lower, key, index, mid)
	}
	return playlistRank(key, upper, index-mid-1, count-mid-1)
}
func playlistEndKey(last string) string {
	if last == "" {
		return "00000000000000000001V"
	}
	if len(last) >= 20 {
		if n, e := strconv.ParseUint(last[:20], 10, 64); e == nil && n < ^uint64(0) {
			return fmt.Sprintf("%020dV", n+1)
		}
	}
	return playlistBetween(last, "")
}
func appendPlaylistEntry(tx *sql.Tx, playlist, entry, item string) error {
	var pos int64
	var last string
	if e := tx.QueryRow(`SELECT COALESCE(max(e.position),0)+1 FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=?`, playlist).Scan(&pos); e != nil {
		return e
	}
	e := tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=p.id WHERE p.token=? ORDER BY e.order_key DESC,e.id DESC LIMIT 1`, playlist).Scan(&last)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	_, e = tx.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,p.id,i.id,?,? FROM catalog_playlists p,catalog_entities i WHERE p.token=? AND i.public_id=pid_blob(?)`, entry, pos, playlistEndKey(last), playlist, item)
	return e
}

func reorderPlaylistWindow(tx *sql.Tx, playlist string, ids []string, after *string) error {
	if len(ids) == 0 || len(ids) > MaxPlaylistEntries {
		return ErrPlaylistCapacity
	}
	seen := map[string]bool{}
	keys := []string{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return requestinput.New("order must contain distinct occurrences")
		}
		seen[id] = true
		var key string
		if e := tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=? AND e.token=?`, playlist, id).Scan(&key); e != nil {
			return e
		}
		keys = append(keys, key)
	}
	if after == nil {
		// Permute these existing slots; all unselected occurrences remain in place.
		sort.Strings(keys)
	} else {
		if seen[*after] {
			return requestinput.New("anchor cannot be one of the moved occurrences")
		}
		lower, upper := "", ""
		if *after != "" {
			if e := tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=? AND e.token=?`, playlist, *after).Scan(&lower); e != nil {
				return e
			}
		}
		e := tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=p.id WHERE p.token=? AND e.order_key>? AND NOT EXISTS(SELECT 1 FROM json_each(?) moved WHERE moved.value=e.token) ORDER BY e.order_key,e.id LIMIT 1`, playlist, lower, idsJSON(ids)).Scan(&upper)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		for i := range keys {
			keys[i] = playlistRank(lower, upper, int64(i), int64(len(keys)))
		}
	}
	for i, id := range ids {
		if _, e := tx.Exec(`UPDATE catalog_playlist_entries SET order_key=? WHERE playlist_id=(SELECT id FROM catalog_playlists WHERE token=?) AND token=?`, keys[i], playlist, id); e != nil {
			return e
		}
	}
	return nil
}
