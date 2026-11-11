package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// The watched mark of a show, season, artist, album or anything else that holds playable items.
//
// For a profile, a holder is watched when the profile can see at least one playable item
// under it and has watched every one it can see. An item the profile cannot see (another
// library, over its rating limit, under a blocked label) counts neither way: it cannot keep
// a show unwatched, and it cannot make one watched. This is the same rule the list of what is
// under the holder follows, so the mark and that list never disagree.
//
// The mark is stored as played = 1 on the profile's own state row for the holder, where the
// watched filter and the watched badge read it without looking at what is under the holder.
// Whatever can change it calls one of two functions in its own transaction:
//
//   - settleWatched, for one profile and some holders: the profile watched or unwatched
//     something under them, or what it may see has changed.
//   - leavesChanged then settleWatchedForAll, for every profile: something playable came
//     under a holder, left it or moved.
//
// RebuildWatchedMarks works every mark out again from nothing.

// watchedAll reports whether a profile has watched everything it may see under a holder, and
// may see something. It is one statement that stops at the first unwatched item it finds, so
// a show someone is part-way through costs a few rows however long it is; only a show that
// is in fact finished is read to its end.
func watchedAll(b *batchTx, acc access, profileID, holder int64) (bool, error) {
	visible := `SELECT 1 FROM items c WHERE {under} AND ` + acc.visible("c")
	unwatched := visible + ` AND NOT EXISTS (SELECT 1 FROM user_item_state w
		WHERE w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1)`
	var all bool
	err := b.queryRow(`SELECT (`+underHolder(`EXISTS (`+visible+`)`, "@id", " OR ")+`) AND NOT (`+
		underHolder(`EXISTS (`+unwatched+`)`, "@id", " OR ")+`)`,
		sql.Named("viewer_profile", profileID), sql.Named("id", holder)).Scan(&all)
	return all, err
}

// settleWatched is the one place a holder's watched mark is written. It works the mark out
// for one profile and each of the holders, stores it where it differs, and tells the
// profile's open apps of each one that changed. Ids that are not holders are passed over.
func settleWatched(b *batchTx, acc access, profileID int64, holders []int64, now int64) error {
	slices.Sort(holders)
	for _, holder := range slices.Compact(holders) {
		var library int64
		var kind ItemKind
		err := b.queryRow(`SELECT library_id, kind FROM items WHERE id = ?`, holder).Scan(&library, &kind)
		if errors.Is(err, sql.ErrNoRows) || err == nil && kind.Playable() {
			continue
		}
		if err != nil {
			return err
		}
		all, err := watchedAll(b, acc, profileID, holder)
		if err != nil {
			return err
		}
		if err := storeWatched(b, profileID, holder, library, all, now); err != nil {
			return err
		}
	}
	return nil
}

// storeWatched writes one profile's mark on one holder where it differs from what is stored,
// and tells the profile's open apps when it did.
func storeWatched(b *batchTx, profileID, holder, library int64, watched bool, now int64) error {
	var res sql.Result
	var err error
	if watched {
		res, err = b.exec(`INSERT INTO user_item_state (profile_id, item_id, played, updated_at) VALUES (?, ?, 1, ?)
			ON CONFLICT (profile_id, item_id) DO UPDATE SET played = 1, updated_at = excluded.updated_at WHERE played = 0`,
			profileID, holder, now)
	} else {
		res, err = b.exec(`UPDATE user_item_state SET played = 0, updated_at = ? WHERE profile_id = ? AND item_id = ? AND played = 1`,
			now, profileID, holder)
	}
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed > 0 {
		return appendChange(b.tx, Change{At: now, Topic: TopicState, Op: OpUpdated, RefID: holder, LibraryID: library, ProfileID: profileID})
	}
	return nil
}

// profileAccess is what a profile may see, for work done on its behalf by someone else.
func profileAccess(b *batchTx, profileID int64) (access, error) {
	var user int64
	if err := b.queryRow(`SELECT user_id FROM profiles WHERE id = ?`, profileID).Scan(&user); err != nil {
		return access{}, err
	}
	return loadAccess(context.Background(), b.tx, Viewer{UserID: user, ProfileID: profileID})
}

// leavesChanged notes that something playable moved between these holders or was split out
// of them: any profile's mark may now differ. settleWatchedForAll acts on it.
func (b *batchTx) leavesChanged(holders ...int64) {
	for _, holder := range holders {
		if holder != 0 {
			b.watchedDirty = append(b.watchedDirty, holder)
		}
	}
}

// leavesLeft notes that a holder only lost items. A profile that holds the mark keeps it
// while it can still see something under the holder, so the show is not read again for it.
func (b *batchTx) leavesLeft(holders ...int64) {
	for _, holder := range holders {
		if holder != 0 {
			b.watchedLeft = append(b.watchedLeft, holder)
		}
	}
}

// leavesArrived notes that a holder gained new items nobody has watched. That can only take
// the mark away, and only from a profile that holds it and can see one of the new items. So
// nothing under the holder is read: one index look for a holder nobody had finished, which
// is every holder during a first import, and one look at the new items for each profile that
// had.
func (b *batchTx) leavesArrived(holder int64, items ...int64) {
	if holder == 0 {
		return
	}
	if b.watchedArrived == nil {
		b.watchedArrived = map[int64][]int64{}
	}
	b.watchedArrived[holder] = append(b.watchedArrived[holder], items...)
}

// settleWatchedForAll settles the holders noted by leavesChanged, leavesLeft, leavesArrived
// and ratingChanged for every profile whose mark could differ. A rating matters only to a
// profile with a rating limit. For a holder that lost or exchanged items those are the
// profiles that hold the mark and the ones that have watched anything under it: a profile
// that has watched nothing there cannot have, or come to have, the mark. It runs at the end
// of the transaction that made the change.
func (b *batchTx) settleWatchedForAll(now int64) error {
	changed, arrived, left, rated := b.watchedDirty, b.watchedArrived, b.watchedLeft, b.watchedRated
	b.watchedDirty, b.watchedArrived, b.watchedLeft, b.watchedRated = nil, nil, nil, nil
	slices.Sort(changed)
	changed = slices.Compact(changed)
	slices.Sort(rated)
	rated = slices.Compact(rated)
	slices.Sort(left)
	left = slices.Compact(left)
	marked := `SELECT profile_id FROM user_item_state WHERE item_id = ?1 AND played = 1`
	anyWatched := marked + ` UNION ` + underHolder(`SELECT s.profile_id FROM items c CROSS JOIN user_item_state s ON s.item_id = c.id
		WHERE {under} AND s.played = 1`, "?1", " UNION ")
	for _, holder := range changed {
		profiles, err := b.queryInts(anyWatched, holder)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			acc, err := profileAccess(b, profile)
			if err != nil {
				return err
			}
			if err := settleWatched(b, acc, profile, []int64{holder}, now); err != nil {
				return err
			}
		}
	}
	for _, holder := range rated {
		if _, found := slices.BinarySearch(changed, holder); found {
			continue
		}
		profiles, err := b.queryInts(anyWatched, holder)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			acc, err := profileAccess(b, profile)
			if err != nil {
				return err
			}
			if acc.maxAge == nil {
				continue
			}
			if err := settleWatched(b, acc, profile, []int64{holder}, now); err != nil {
				return err
			}
		}
	}
	for _, holder := range left {
		if _, found := slices.BinarySearch(changed, holder); found {
			continue
		}
		_, settled := slices.BinarySearch(rated, holder) // for the profiles with a rating limit
		holding, err := b.queryInts(marked, holder)
		if err != nil {
			return err
		}
		profiles, err := b.queryInts(anyWatched, holder)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			acc, err := profileAccess(b, profile)
			if err != nil {
				return err
			}
			if settled && acc.maxAge != nil {
				continue
			}
			if slices.Contains(holding, profile) {
				var sees bool
				if err := b.queryRow(`SELECT `+underHolder(`EXISTS (SELECT 1 FROM items c WHERE {under} AND `+acc.visible("c")+`)`, "?1", " OR "), holder).Scan(&sees); err != nil {
					return err
				}
				if sees {
					continue
				}
			}
			if err := settleWatched(b, acc, profile, []int64{holder}, now); err != nil {
				return err
			}
		}
	}
	for holder, items := range arrived {
		if _, found := slices.BinarySearch(changed, holder); found || len(items) == 0 {
			continue
		}
		profiles, err := b.queryInts(marked, holder)
		if err != nil || len(profiles) == 0 {
			if err != nil {
				return err
			}
			continue
		}
		var library int64
		if err := b.queryRow(`SELECT library_id FROM items WHERE id = ?`, holder).Scan(&library); err != nil {
			return err
		}
		for _, profile := range profiles {
			acc, err := profileAccess(b, profile)
			if err != nil {
				return err
			}
			var sees bool
			if err := b.queryRow(`SELECT EXISTS (SELECT 1 FROM items c WHERE c.id IN ` + intList(items) + ` AND ` + acc.visible("c") + `)`).Scan(&sees); err != nil {
				return err
			}
			if sees {
				if err := storeWatched(b, profile, holder, library, false, now); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// queryInts reads a column of ids with a statement kept for the transaction.
func (b *batchTx) queryInts(query string, args ...any) ([]int64, error) {
	stmt, err := b.stmt(query)
	if err != nil {
		return nil, err
	}
	rows, err := stmt.Query(args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// holdersAround lists a holder, the holders it sits under and the holders under it: every
// mark that marking the whole of it watched or unwatched can change.
func holdersAround(b *batchTx, holder int64) ([]int64, error) {
	return queryInts(b.tx, `SELECT ?1 UNION SELECT parent_id FROM items WHERE id = ?1 AND parent_id IS NOT NULL
		UNION SELECT root_id FROM items WHERE id = ?1 AND root_id IS NOT NULL
		UNION SELECT id FROM items WHERE root_id = ?1 AND kind NOT IN `+playableKindsSQL+`
		UNION SELECT id FROM items WHERE parent_id = ?1 AND kind NOT IN `+playableKindsSQL, holder)
}

// rebuildWatched works out every mark of one profile from nothing: each holder that holds
// the mark now, and each holder above something the profile has watched.
func rebuildWatched(b *batchTx, profileID int64, now int64) error {
	acc, err := profileAccess(b, profileID)
	if err != nil {
		return err
	}
	holders, err := queryInts(b.tx, `SELECT s.item_id FROM user_item_state s JOIN items i ON i.id = s.item_id
			WHERE s.profile_id = ?1 AND s.played = 1 AND i.kind NOT IN `+playableKindsSQL+`
		UNION SELECT c.parent_id FROM user_item_state s JOIN items c ON c.id = s.item_id
			WHERE s.profile_id = ?1 AND s.played = 1 AND c.kind IN `+playableKindsSQL+` AND c.parent_id IS NOT NULL
		UNION SELECT c.root_id FROM user_item_state s JOIN items c ON c.id = s.item_id
			WHERE s.profile_id = ?1 AND s.played = 1 AND c.kind IN `+playableKindsSQL+` AND c.root_id IS NOT NULL`, profileID)
	if err != nil {
		return err
	}
	return settleWatched(b, acc, profileID, holders, now)
}

// RebuildWatchedMarks works out every profile's watched marks again from what each has
// watched, one profile to a transaction. Nothing needs it in ordinary running: every change
// settles its own marks. It is there to prove that, and to put right a database whose marks
// were ever written wrongly.
func (d *DB) RebuildWatchedMarks(ctx context.Context) error {
	rows, err := d.read.QueryContext(ctx, `SELECT id FROM profiles ORDER BY id`)
	if err != nil {
		return err
	}
	var all []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		all = append(all, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, profile := range all {
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			b := newBatchTx(tx)
			defer b.close()
			err := rebuildWatched(b, profile, time.Now().UnixMilli())
			if errors.Is(err, sql.ErrNoRows) { // the profile was deleted meanwhile
				return nil
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
