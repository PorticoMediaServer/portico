// Package personalstate owns the shared watched-state expression. SQL inputs
// are source-controlled expressions, never values from an API request.
package personalstate

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

func Stamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

// SQL uses each supplied expression exactly once, so callers can bind a profile
// and item using ordinary placeholders. `item` must be a SQL expression
// yielding the integer entity id (catalog_entities.id). Explicit intent wins;
// the latest container write wins, with the
// narrower season breaking a timestamp tie. Unknown added_ms is not evidence that an item existed at the watermark.
func SQL(profile, item string) string {
	return watchedSQL(profile, item)
}

// CompactSQL preserves the public-ID personal boundary while reading only
// published compact catalogue membership and added-at facts.
func CompactSQL(profile, item string) string {
	return watchedSQL(profile, item)
}

func watchedSQL(profile, item string) string {
	candidates := []struct {
		kind, id string
		priority int
	}{{"season", "cep.season_id", 0}, {"show", "cep.show_id", 1}, {"album", "cso.album_id", 1}, {"book", "cbf.book_id", 1}}
	// The old items/episodes/songs/book_files source is gone: both entry
	// points read the compact catalogue. Each container id is one primary-key
	// seek from the item's entity, and every comparison is integer to integer.
	source := `catalog_entities it
 LEFT JOIN catalog_episodes cep ON cep.entity_id=it.id
 LEFT JOIN catalog_songs cso ON cso.entity_id=it.id
 LEFT JOIN catalog_book_files cbf ON cbf.entity_id=it.id`
	parts := []string{}
	for _, c := range candidates {
		parts = append(parts, `SELECT watched,watermark,cleared_through,`+strconv.Itoa(c.priority)+` AS priority FROM container_personal_state WHERE profile_id=wp.profile AND kind='`+c.kind+`' AND container_id=`+c.id)
	}
	return `(SELECT (WITH defaults AS MATERIALIZED (` + strings.Join(parts, ` UNION ALL `) + `) SELECT COALESCE(
 (SELECT wi.watched FROM personal_watched_intents wi WHERE wi.profile_id=wp.profile AND wi.item_id=it.id AND wi.authored_at>COALESCE((SELECT max(cleared_through) FROM defaults),'')),
 (SELECT watched FROM defaults WHERE it.added_ms IS NOT NULL AND julianday(it.added_ms/1000.0,'unixepoch')<=julianday(watermark) ORDER BY watermark DESC,priority LIMIT 1),0))
 FROM ` + source + ` CROSS JOIN (SELECT ` + profile + ` AS profile) wp WHERE it.id=` + item + `)`
}

// Write records the profile's explicit watched intent for the item, named by
// its integer entity id (catalog_entities.id) as every personal-state helper
// takes it.
func Write(tx *sql.Tx, profile string, entity int64, watched bool) error {
	return WriteAt(tx, profile, entity, watched, time.Now())
}

// WriteAt preserves when accepted playback evidence happened, including an
// offline occurrence. Explicit user actions still use the server clock via Write.
func WriteAt(tx *sql.Tx, profile string, entity int64, watched bool, at time.Time) error {
	_, err := tx.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES(?,?,?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=excluded.watched,authored_at=excluded.authored_at`, profile, entity, watched, at.UTC().Format("2006-01-02T15:04:05.000000000Z"))
	return err
}
