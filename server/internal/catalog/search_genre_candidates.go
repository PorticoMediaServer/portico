package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

const genrePositionGap = int64(1 << 48)

type genreTarget struct {
	Entity           int64
	Public           []byte
	Kind             int
	Rating           float64
	Order            string
	Backdrop, Poster []byte
}
type genreCandidateCursor struct {
	After, Before int64
	Repack        int
	Rank          int64
}

func loadGenreTarget(ctx context.Context, tx *sql.Tx, target int64, kind int) (genreTarget, error) {
	t := genreTarget{Entity: target, Kind: kind}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(b.rating_max,0),b.sort_key,e.public_id FROM catalog_browse_rows b JOIN catalog_entities e ON e.id=b.entity_id WHERE b.entity_id=?`, target).Scan(&t.Rating, &t.Order, &t.Public); err != nil {
		return t, err
	}
	artKind := "item"
	if kind == 2 {
		artKind = "show"
	}
	for role, name := range []string{"backdrop", "poster"} {
		var digest string
		err := tx.QueryRowContext(ctx, `SELECT a.digest FROM artwork_selections a JOIN artwork_sources s ON s.digest=a.digest JOIN artwork_blobs sb ON sb.digest=s.small_blob AND sb.status='ready' JOIN artwork_blobs lb ON lb.digest=s.large_blob AND lb.status='ready' WHERE a.kind=? AND a.entity_id=? AND a.role=? AND a.subject=''`, artKind, target, name).Scan(&digest)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return t, err
		}
		raw, err := hex.DecodeString(digest)
		if err != nil || len(raw) != 32 {
			return t, ErrFacetBaseInput
		}
		if role == 0 {
			t.Backdrop = raw
		} else {
			t.Poster = raw
		}
	}
	return t, nil
}
func genreRankBefore(t genreTarget, rating float64, key string, entity int64) bool {
	return t.Rating > rating || t.Rating == rating && (t.Order < key || t.Order == key && t.Entity < entity)
}

// One quantum examines <=8 candidate keys/256KiB or repacks <=8 integer keys.
// A single wide key is the explicit O(B) exception; no slice retains title keys.
func stepGenreCandidate(ctx context.Context, tx *sql.Tx, base, value, target int64, kind, role int, c *genreCandidateCursor) (bool, error) {
	if c.Repack > 0 {
		return stepGenreRepack(ctx, tx, base, value, role, c)
	}
	t, err := loadGenreTarget(ctx, tx, target, kind)
	if err != nil {
		return false, err
	}
	source := t.Backdrop
	if role == 2 {
		source = t.Poster
	}
	if source == nil {
		return true, nil
	}
	var present bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_search_genre_candidates WHERE base_id=? AND entity_id=? AND value_id=? AND role=?)`, base, target, value, role).Scan(&present); err != nil {
		return false, err
	}
	if present {
		return true, nil
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT CASE ? WHEN 1 THEN backdrops ELSE posters END FROM catalog_search_genre_ready WHERE base_id=? AND value_id=?`, role, base, value).Scan(&count); err != nil {
		return false, err
	}
	if c.After == 0 && count == 100 {
		var entity int64
		var rating float64
		var key string
		if err = tx.QueryRowContext(ctx, `SELECT c.entity_id,t.rating,t.sort_key FROM catalog_search_genre_candidates c JOIN catalog_search_genre_targets t ON t.base_id=c.base_id AND t.entity_id=c.entity_id WHERE c.base_id=? AND c.value_id=? AND c.role=? ORDER BY c.position DESC LIMIT 1`, base, value, role).Scan(&entity, &rating, &key); err != nil {
			return false, err
		}
		if !genreRankBefore(t, rating, key, entity) {
			return true, nil
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.position,c.entity_id,t.rating,t.sort_key FROM catalog_search_genre_candidates c JOIN catalog_search_genre_targets t ON t.base_id=c.base_id AND t.entity_id=c.entity_id WHERE c.base_id=? AND c.value_id=? AND c.role=? AND c.position>? ORDER BY c.position LIMIT 8`, base, value, role, c.After)
	if err != nil {
		return false, err
	}
	n, bytes := 0, len(t.Order)
	after := int64(0)
	started := time.Now()
	for rows.Next() {
		var p, entity int64
		var rating float64
		var key string
		if err = rows.Scan(&p, &entity, &rating, &key); err != nil {
			rows.Close()
			return false, err
		}
		n++
		bytes += len(key)
		if genreRankBefore(t, rating, key, entity) {
			after = p
			break
		}
		c.After, c.Before = p, p
		if bytes >= 256<<10 || time.Since(started) >= 8*time.Millisecond {
			rows.Close()
			return false, nil
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if after == 0 && n == 8 {
		return false, nil
	}
	position := c.Before + genrePositionGap
	if after > 0 {
		position = c.Before + (after-c.Before)/2
	}
	if position == c.Before || position == after {
		c.Repack = 1
		c.After = 0
		c.Before = 0
		return false, nil
	}
	if count == 100 {
		var last, lastEntity int64
		if err = tx.QueryRowContext(ctx, `SELECT position,entity_id FROM catalog_search_genre_candidates WHERE base_id=? AND value_id=? AND role=? ORDER BY position DESC LIMIT 1`, base, value, role).Scan(&last, &lastEntity); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_search_genre_candidates WHERE base_id=? AND value_id=? AND role=? AND position=?`, base, value, role, last); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_search_genre_targets WHERE base_id=? AND entity_id=? AND NOT EXISTS(SELECT 1 FROM catalog_search_genre_candidates WHERE base_id=? AND entity_id=?)`, base, lastEntity, base, lastEntity); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_search_genre_targets(base_id,entity_id,public_id,kind,rating,sort_key,backdrop,poster) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(base_id,entity_id) DO NOTHING`, base, target, t.Public, kind, t.Rating, t.Order, t.Backdrop, t.Poster); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_search_genre_candidates(base_id,value_id,role,position,entity_id) VALUES(?,?,?,?,?)`, base, value, role, position, target)
	return err == nil, err
}
func stepGenreRepack(ctx context.Context, tx *sql.Tx, base, value int64, role int, c *genreCandidateCursor) (bool, error) {
	query := `SELECT position FROM catalog_search_genre_candidates WHERE base_id=? AND value_id=? AND role=? AND position>0 ORDER BY position LIMIT 8`
	if c.Repack == 2 {
		query = `SELECT position FROM catalog_search_genre_candidates WHERE base_id=? AND value_id=? AND role=? AND position<0 ORDER BY position DESC LIMIT 8`
	}
	rows, err := tx.QueryContext(ctx, query, base, value, role)
	if err != nil {
		return false, err
	}
	positions := make([]int64, 0, 8)
	for rows.Next() {
		var p int64
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return false, err
		}
		positions = append(positions, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(positions) == 0 {
		if c.Repack == 1 {
			c.Repack = 2
			c.Rank = 1
		} else {
			*c = genreCandidateCursor{}
		}
		return false, nil
	}
	for _, p := range positions {
		next := -p
		if c.Repack == 2 {
			next = c.Rank * genrePositionGap
			c.Rank++
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_search_genre_candidates SET position=? WHERE base_id=? AND value_id=? AND role=? AND position=?`, next, base, value, role, p); err != nil {
			return false, err
		}
	}
	return false, nil
}
