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

// watchedUnder counts the playable items under a holder that a profile may see, and how many
// of those it has watched.
func watchedUnder(b *batchTx, acc access, profileID, holder int64) (leaves, watched int, err error) {
	err = b.queryRow(`SELECT count(*), count(played) FROM (`+underHolder(`SELECT c.id, w.item_id AS played FROM items c
		LEFT JOIN user_item_state w ON w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1
		WHERE {under} AND `+acc.visible("c"), "@id", " UNION ")+`)`,
		sql.Named("viewer_profile", profileID), sql.Named("id", holder)).Scan(&leaves, &watched)
	return leaves, watched, err
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
		leaves, watched, err := watchedUnder(b, acc, profileID, holder)
		if err != nil {
			return err
		}
		var res sql.Result
		if leaves > 0 && watched == leaves {
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
			if err := appendChange(b.tx, Change{At: now, Topic: TopicState, Op: OpUpdated, RefID: holder, LibraryID: library, ProfileID: profileID}); err != nil {
				return err
			}
		}
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

// leavesChanged notes that something playable came under these holders, left them or moved,
// or that their own state rows were put back. settleWatchedForAll acts on it.
func (b *batchTx) leavesChanged(holders ...int64) {
	for _, holder := range holders {
		if holder != 0 {
			b.watchedDirty = append(b.watchedDirty, holder)
		}
	}
}

// settleWatchedForAll settles the holders noted by leavesChanged for every profile whose
// mark could differ: the ones that hold the mark, and the ones that have watched anything
// under the holder. A profile that has watched nothing there cannot have, or come to have,
// the mark. It runs at the end of the transaction that made the change.
func (b *batchTx) settleWatchedForAll(now int64) error {
	holders := b.watchedDirty
	b.watchedDirty = nil
	slices.Sort(holders)
	for _, holder := range slices.Compact(holders) {
		profiles, err := queryInts(b.tx, `SELECT profile_id FROM user_item_state WHERE item_id = ?1 AND played = 1
			UNION `+underHolder(`SELECT s.profile_id FROM items c CROSS JOIN user_item_state s ON s.item_id = c.id WHERE {under} AND s.played = 1`,
			"?1", " UNION "), holder)
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
	return nil
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
