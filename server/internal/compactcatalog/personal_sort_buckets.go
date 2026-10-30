package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// StepPersonalSortBuckets advances one of the six durable streams: a personal
// item edit, membership edit, browse entity edit, profile/class intersection,
// class/profile intersection, or initial source backfill. Every stream keysets
// at most limit identities; source triggers only enqueue the first key.
func StepPersonalSortBuckets(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("invalid personal sort bucket limit")
	}
	if limit > 100 {
		limit = 100
	}
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM catalog_personal_sort_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	steps := [6]func(context.Context, *sql.Tx, int) (int, error){stepPersonalSortItem, stepPersonalSortMember, stepPersonalSortEntity, stepPersonalVisibilityProfile, stepPersonalVisibilityClass, stepPersonalSortBackfill}
	for i := range steps {
		kind := (turn + i) % len(steps)
		n, err := steps[kind](ctx, tx, limit)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_state SET turn=? WHERE id=1`, (kind+1)%len(steps))
			return n, err
		}
	}
	return 0, nil
}

func stepPersonalSortItem(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var profile string
	var item int64
	var sequence, cursorSequence, after int64
	err := tx.QueryRowContext(ctx, `SELECT profile_id,item_id,sequence,cursor_sequence,entity_cursor FROM catalog_personal_sort_item_jobs ORDER BY profile_id,item_id LIMIT 1`).Scan(&profile, &item, &sequence, &cursorSequence, &after)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		after = 0
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_item_jobs SET cursor_sequence=sequence,entity_cursor=0 WHERE profile_id=? AND item_id=?`, profile, item); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH current AS (
	 SELECT m.entity_id FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=? AND m.entity_id>? GROUP BY m.entity_id ORDER BY m.entity_id LIMIT ?
	), prior AS (
	 SELECT entity_id FROM catalog_personal_sort_members INDEXED BY catalog_personal_sort_member_item WHERE profile_id=? AND item_id=? AND entity_id>? ORDER BY entity_id LIMIT ?
	) SELECT entity_id FROM current UNION SELECT entity_id FROM prior UNION SELECT entity_id FROM catalog_browse_rows WHERE entity_id=? AND entity_id>? ORDER BY entity_id LIMIT ?`, item, after, limit, profile, item, after, limit, item, after, limit)
	if err != nil {
		return 0, err
	}
	entities := []int64{}
	for rows.Next() {
		var entity int64
		if err = rows.Scan(&entity); err != nil {
			break
		}
		entities = append(entities, entity)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, entity := range entities {
		if err = reconcilePersonalSortMember(ctx, tx, profile, item, entity); err != nil {
			return 0, err
		}
	}
	if len(entities) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_sort_item_jobs WHERE profile_id=? AND item_id=?`, profile, item)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_item_jobs SET entity_cursor=? WHERE profile_id=? AND item_id=? AND sequence=?`, entities[len(entities)-1], profile, item, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(entities) == 0 {
		return 1, nil
	}
	return len(entities), nil
}

func stepPersonalSortMember(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var item, sequence, cursorSequence int64
	var after string
	err := tx.QueryRowContext(ctx, `SELECT item_id,sequence,cursor_sequence,profile_cursor FROM catalog_personal_sort_member_jobs ORDER BY item_id LIMIT 1`).Scan(&item, &sequence, &cursorSequence, &after)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		after = ""
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_member_jobs SET cursor_sequence=sequence,profile_cursor='' WHERE item_id=?`, item); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH sort_items AS (
 SELECT profile_id,item_id FROM personal_items WHERE item_id=?1 AND profile_id>?2 ORDER BY profile_id LIMIT ?3
), plays AS (
 SELECT profile_id,item_id FROM personal_play_counts WHERE item_id=?1 AND profile_id>?2 ORDER BY profile_id LIMIT ?3
), current AS (SELECT profile_id,item_id FROM sort_items UNION SELECT profile_id,item_id FROM plays ORDER BY profile_id,item_id LIMIT ?3), prior AS (
 SELECT DISTINCT profile_id,item_id FROM catalog_personal_sort_members INDEXED BY catalog_personal_sort_member_item_id WHERE item_id=?1 AND profile_id>?2 ORDER BY profile_id,item_id LIMIT ?3
) SELECT profile_id,item_id FROM current UNION SELECT profile_id,item_id FROM prior ORDER BY profile_id,item_id LIMIT ?3`, item, after, limit)
	if err != nil {
		return 0, err
	}
	type key struct {
		profile string
		item    int64
	}
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.profile, &k.item); err != nil {
			break
		}
		keys = append(keys, k)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_personal_sort_item_jobs(profile_id,item_id) VALUES(?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET sequence=sequence+1`, k.profile, k.item); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_sort_member_jobs WHERE item_id=?`, item)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_member_jobs SET profile_cursor=? WHERE item_id=? AND sequence=?`, keys[len(keys)-1].profile, item, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func stepPersonalSortEntity(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var entity, sequence, cursorSequence int64
	var after string
	err := tx.QueryRowContext(ctx, `SELECT entity_id,sequence,cursor_sequence,profile_cursor FROM catalog_personal_sort_entity_jobs ORDER BY entity_id LIMIT 1`).Scan(&entity, &sequence, &cursorSequence, &after)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		after = ""
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_entity_jobs SET cursor_sequence=sequence,profile_cursor='' WHERE entity_id=?`, entity); err != nil {
			return 0, err
		}
	}
	// An item stream can run before its entity has a browse row and drop its
	// job without a member; the row insert then arrives here. Rebuild those
	// members from the personal source rather than only refreshing members
	// that already exist, so the late row still gets the rating, last-played
	// date and play count the item stream carried.
	rows, err := tx.QueryContext(ctx, `WITH current AS (
 SELECT DISTINCT profile_id FROM catalog_personal_sort_members INDEXED BY catalog_personal_sort_member_entity WHERE entity_id=?1 AND profile_id>?2 ORDER BY profile_id LIMIT ?3
), prior AS (
 SELECT profile_id FROM catalog_personal_sort_rows INDEXED BY catalog_personal_sort_row_entity WHERE entity_id=?1 AND profile_id>?2 ORDER BY profile_id LIMIT ?3
), source_items AS (
 SELECT profile_id FROM personal_items WHERE item_id=?1 AND profile_id>?2 ORDER BY profile_id LIMIT ?3
), source_plays AS (
 SELECT profile_id FROM personal_play_counts WHERE item_id=?1 AND profile_id>?2 ORDER BY profile_id LIMIT ?3
) SELECT profile_id FROM current UNION SELECT profile_id FROM prior UNION SELECT profile_id FROM source_items UNION SELECT profile_id FROM source_plays ORDER BY profile_id LIMIT ?3`, entity, after, limit)
	if err != nil {
		return 0, err
	}
	profiles := []string{}
	for rows.Next() {
		var profile string
		if err = rows.Scan(&profile); err != nil {
			break
		}
		profiles = append(profiles, profile)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, profile := range profiles {
		if err = reconcilePersonalSortMember(ctx, tx, profile, entity, entity); err != nil {
			return 0, err
		}
	}
	if len(profiles) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_sort_entity_jobs WHERE entity_id=?`, entity)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_entity_jobs SET profile_cursor=? WHERE entity_id=? AND sequence=?`, profiles[len(profiles)-1], entity, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(profiles) == 0 {
		return 1, nil
	}
	return len(profiles), nil
}

func stepPersonalSortBackfill(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var afterProfile string
	var afterItem int64
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT profile_cursor,item_cursor,backfill_done FROM catalog_personal_sort_state WHERE id=1`).Scan(&afterProfile, &afterItem, &done); err != nil {
		return 0, err
	}
	if done {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `WITH sort_items AS(SELECT profile_id,item_id FROM personal_items WHERE (profile_id,item_id)>(?1,?2) ORDER BY profile_id,item_id LIMIT ?3),plays AS(SELECT profile_id,item_id FROM personal_play_counts WHERE (profile_id,item_id)>(?1,?2) ORDER BY profile_id,item_id LIMIT ?3) SELECT profile_id,item_id FROM sort_items UNION SELECT profile_id,item_id FROM plays ORDER BY profile_id,item_id LIMIT ?3`, afterProfile, afterItem, limit)
	if err != nil {
		return 0, err
	}
	type key struct {
		profile string
		item    int64
	}
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.profile, &k.item); err != nil {
			break
		}
		keys = append(keys, k)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_state SET backfill_done=1 WHERE id=1`)
		return 1, err
	}
	for _, k := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_personal_sort_item_jobs(profile_id,item_id) VALUES(?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET sequence=sequence+1`, k.profile, k.item); err != nil {
			return 0, err
		}
	}
	last := keys[len(keys)-1]
	_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_sort_state SET profile_cursor=?,item_cursor=? WHERE id=1`, last.profile, last.item)
	return len(keys), err
}

func reconcilePersonalSortMember(ctx context.Context, tx *sql.Tx, profile string, item int64, entity int64) error {
	var rating sql.NullFloat64
	var last string
	var plays int
	err := tx.QueryRowContext(ctx, `SELECT p.rating,COALESCE(p.last_played_at,''),COALESCE(c.plays,0) FROM (SELECT ? AS profile,? AS item) k LEFT JOIN personal_items p ON p.profile_id=k.profile AND p.item_id=k.item LEFT JOIN personal_play_counts c ON c.profile_id=k.profile AND c.item_id=k.item
 WHERE (p.item_id IS NOT NULL OR c.item_id IS NOT NULL) AND (EXISTS(SELECT 1 FROM catalog_browse_memberships m WHERE m.entity_id=? AND m.item_id=k.item) OR (k.item=? AND EXISTS(SELECT 1 FROM catalog_browse_rows WHERE entity_id=k.item)))`, profile, item, entity, entity).Scan(&rating, &last, &plays)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	found := err == nil
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_sort_members WHERE profile_id=? AND entity_id=? AND item_id=?`, profile, entity, item); err != nil {
		return err
	}
	if found {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_personal_sort_members(profile_id,entity_id,item_id,rating,last_played_at,play_count) VALUES(?,?,?,?,?,?)`, profile, entity, item, rating, last, plays); err != nil {
			return err
		}
	}
	return refreshPersonalSortEntity(ctx, tx, profile, entity)
}

func personalRatingKey(rating sql.NullFloat64) string {
	if !rating.Valid {
		return "0"
	}
	return strconv.FormatInt(int64(rating.Float64), 10)
}
func personalDateKeys(last string) (string, string) {
	month, year := "", ""
	if len(last) >= 7 {
		month = last[:7]
	}
	if len(last) >= 4 {
		year = last[:4]
	}
	return month, year
}

func refreshPersonalSortEntity(ctx context.Context, tx *sql.Tx, profile string, entity int64) error {
	var library, kind int
	var sortKey string
	rowErr := tx.QueryRowContext(ctx, `SELECT library_id,kind,sort_key FROM catalog_browse_rows WHERE entity_id=?`, entity).Scan(&library, &kind, &sortKey)
	if rowErr != nil && rowErr != sql.ErrNoRows {
		return rowErr
	}
	var has bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_personal_sort_members WHERE profile_id=? AND entity_id=?)`, profile, entity).Scan(&has); err != nil {
		return err
	}
	var oldLibrary, oldKind int
	var oldRating, oldMonth, oldYear, oldLast, oldSortKey string
	var oldRatingValue, oldPlays int
	oldErr := tx.QueryRowContext(ctx, `SELECT library_id,kind,rating_key,last_month,last_year,rating,last_played_at,play_count,sort_key FROM catalog_personal_sort_rows WHERE profile_id=? AND entity_id=?`, profile, entity).Scan(&oldLibrary, &oldKind, &oldRating, &oldMonth, &oldYear, &oldRatingValue, &oldLast, &oldPlays, &oldSortKey)
	if oldErr != nil && oldErr != sql.ErrNoRows {
		return oldErr
	}
	adjustOld := func() error {
		if oldErr == nil {
			return adjustPersonalSortBuckets(ctx, tx, profile, oldLibrary, oldKind, [3]string{oldRating, oldMonth, oldYear}, -1)
		}
		return nil
	}
	if !has || rowErr == sql.ErrNoRows {
		if err := adjustOld(); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_personal_sort_rows WHERE profile_id=? AND entity_id=?`, profile, entity)
		return err
	}
	var rating sql.NullFloat64
	var last string
	if err := tx.QueryRowContext(ctx, `SELECT rating FROM catalog_personal_sort_members INDEXED BY catalog_personal_sort_member_rating WHERE profile_id=? AND entity_id=? AND rating IS NOT NULL ORDER BY rating DESC LIMIT 1`, profile, entity).Scan(&rating); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT last_played_at FROM catalog_personal_sort_members INDEXED BY catalog_personal_sort_member_last WHERE profile_id=? AND entity_id=? ORDER BY last_played_at DESC LIMIT 1`, profile, entity).Scan(&last); err != nil && err != sql.ErrNoRows {
		return err
	}
	var plays int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT total FROM catalog_personal_play_totals WHERE profile_id=? AND entity_id=?),0)`, profile, entity).Scan(&plays); err != nil {
		return err
	}
	if oldErr == nil && oldLibrary == library && oldKind == kind && oldRatingValue == int(rating.Float64) && oldLast == last && oldPlays == plays && oldSortKey == sortKey {
		return nil
	}
	if err := adjustOld(); err != nil {
		return err
	}
	month, year := personalDateKeys(last)
	values := [3]string{personalRatingKey(rating), month, year}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_personal_sort_rows(profile_id,entity_id,library_id,kind,rating_key,last_month,last_year,rating,last_played_at,play_count,sort_key) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(profile_id,entity_id) DO UPDATE SET library_id=excluded.library_id,kind=excluded.kind,rating_key=excluded.rating_key,last_month=excluded.last_month,last_year=excluded.last_year,rating=excluded.rating,last_played_at=excluded.last_played_at,play_count=excluded.play_count,sort_key=excluded.sort_key`, profile, entity, library, kind, values[0], values[1], values[2], int(rating.Float64), last, plays, sortKey); err != nil {
		return err
	}
	return adjustPersonalSortBuckets(ctx, tx, profile, library, kind, values, 1)
}

func adjustPersonalSortBuckets(ctx context.Context, tx *sql.Tx, profile string, library, kind int, values [3]string, delta int) error {
	for offset, value := range values {
		axis := offset + 7
		if delta > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_personal_sort_buckets(profile_id,library_id,kind,axis,value,total) VALUES(?,?,?,?,?,1) ON CONFLICT(profile_id,library_id,kind,axis,value) DO UPDATE SET total=total+1`, profile, library, kind, axis, value); err != nil {
				return err
			}
		}
		if delta < 0 {
			result, err := tx.ExecContext(ctx, `UPDATE catalog_personal_sort_buckets SET total=total-1 WHERE profile_id=? AND library_id=? AND kind=? AND axis=? AND value=?`, profile, library, kind, axis, value)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n != 1 {
				return fmt.Errorf("missing personal sort bucket")
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_personal_sort_buckets WHERE profile_id=? AND library_id=? AND kind=? AND axis=? AND value=? AND total=0`, profile, library, kind, axis, value); err != nil {
				return err
			}
		}
	}
	return nil
}
