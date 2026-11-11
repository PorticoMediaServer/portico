package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// Automatic collections (definition 04, rule 56). In a library with "Group films from the same
// series" on, which it is unless the owner turns it off, a collection appears once two films
// of one series are there, named without "Collection", and later films join it.
//
// How it is kept:
//   - A film's series is one of its provider ids: provider "tmdb-collection", id 2344. That is
//     how a second film finds the first, by the item_external_ids_lookup index.
//   - The collection is an items row of kind collection with a provider key, "tmdb:collection:2344",
//     which is what makes it automatic and what later films find it by.
//   - The owner's rename locks its title and a change of its films locks its members (see
//     collections_admin.go). Either marks the collection as the owner's: turning "Group films
//     from the same series" off removes the automatic collections nobody changed, and leaves
//     a changed one as an ordinary collection.
//   - A collection the owner deletes is remembered in the library's options, under
//     "removedCollections", by its key, and never made again. A film the owner takes out of
//     one is remembered under "removedFromCollections", the collection's key to the film's
//     keys: only that film stays out, and later films still join.

// SeriesRef is the series a provider says a film belongs to.
type SeriesRef struct {
	Provider string // "tmdb"
	ID       string // the provider's id of the series
	Name     string // as the provider names it: "The Matrix Collection"
}

func (s SeriesRef) key() string        { return s.Provider + ":collection:" + s.ID }
func (s SeriesRef) idProvider() string { return s.Provider + "-collection" }

// seriesTitle is a collection's name without the word every provider adds.
func seriesTitle(name string) string {
	name = strings.TrimSpace(name)
	if trimmed := strings.TrimSpace(strings.TrimSuffix(name, " Collection")); trimmed != "" {
		return trimmed
	}
	return name
}

// joinSeries puts a film in its series' collection, making the collection when this is the
// second film of the series in the library. It is called inside applyDetails.
func joinSeries(b *batchTx, library, film int64, s SeriesRef, now int64) error {
	var group, removed, filmOut bool
	if err := b.queryRow(`SELECT ifnull(l.options ->> '$.groupSeries', 1),
		EXISTS (SELECT 1 FROM json_each(l.options, '$.removedCollections') WHERE value = ?1),
		EXISTS (SELECT 1 FROM json_each(l.options -> '$.removedFromCollections', '$."' || ?1 || '"') r
			JOIN item_keys k ON k.key = r.value AND k.kind = ?3 AND k.library_id = l.id WHERE k.item_id = ?4)
		FROM libraries l WHERE l.id = ?2`, s.key(), library, KeyProvider, film).Scan(&group, &removed, &filmOut); err != nil {
		return err
	}
	if !group || removed || filmOut {
		return nil
	}
	var collection int64
	err := b.queryRow(`SELECT k.item_id FROM item_keys k WHERE k.library_id = ? AND k.kind = ? AND k.key = ?`, library, KeyProvider,
		s.key()).Scan(&collection)
	switch {
	case err == nil:
		res, err := b.exec(`INSERT INTO item_relations (item_id, kind, related_item_id, position)
			SELECT ?1, ?2, ?3, ifnull(max(position), 0) + 1 FROM item_relations WHERE item_id = ?1 AND kind = ?2 ON CONFLICT DO NOTHING`,
			collection, RelationCollectionMember, film)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: collection, LibraryID: library})
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	films, err := queryInts(b.tx, `SELECT i.id FROM item_external_ids e JOIN items i ON i.id = e.item_id WHERE e.provider = ? AND e.external_id = ?
		AND i.library_id = ? AND i.kind = ? AND i.parent_id IS NULL ORDER BY ifnull(i.release_date, i.year), i.id`,
		s.idProvider(), s.ID, library, KindMovie)
	if err != nil || len(films) < 2 {
		return err
	}
	var language sql.NullString
	if err := b.queryRow(`SELECT language FROM libraries WHERE id = ?`, library).Scan(&language); err != nil {
		return err
	}
	collection, err = b.insertItem(itemRow{ImportItem: &ImportItem{Kind: KindCollection, Title: seriesTitle(s.Name), Language: language.String},
		libraryID: library, publicID: NewID("itm"), addedAt: now})
	if err != nil {
		return err
	}
	if err := b.insertItemKey(library, ItemKey{Kind: KeyProvider, Key: s.key()}, collection); err != nil {
		return err
	}
	for n, id := range films {
		if _, err := b.exec(`INSERT INTO item_relations (item_id, kind, related_item_id, position) VALUES (?, ?, ?, ?)`,
			collection, RelationCollectionMember, id, n+1); err != nil {
			return err
		}
	}
	return appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpCreated, RefID: collection, LibraryID: library})
}

// leaveSeries takes a film out of the automatic collections it is in, before its match
// changes: it is another film now. A collection left holding nothing goes. One whose films
// the owner set keeps the film.
func leaveSeries(b *batchTx, library, film, now int64) error {
	collections, err := queryInts(b.tx, `SELECT r.item_id FROM item_relations r JOIN items c ON c.id = r.item_id
		WHERE r.related_item_id = ? AND r.kind = ? AND EXISTS (SELECT 1 FROM item_keys k WHERE k.item_id = c.id AND k.kind = ?)
		AND NOT EXISTS (SELECT 1 FROM json_each(c.locked_fields) WHERE value = ?)`, film, RelationCollectionMember, KeyProvider, lockedMembers)
	if err != nil {
		return err
	}
	for _, collection := range collections {
		if _, err := b.exec(`DELETE FROM item_relations WHERE item_id = ? AND kind = ? AND related_item_id = ?`,
			collection, RelationCollectionMember, film); err != nil {
			return err
		}
		var held bool
		if err := b.queryRow(`SELECT EXISTS (SELECT 1 FROM item_relations WHERE item_id = ? AND kind = ?)`, collection,
			RelationCollectionMember).Scan(&held); err != nil {
			return err
		}
		if !held {
			if err := removeItem(b, library, collection, now); err != nil {
				return err
			}
		} else if err := appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: collection, LibraryID: library}); err != nil {
			return err
		}
	}
	return nil
}

// rememberRemoved records, as the owner deletes a collection, that an automatic one must not
// be made again.
func rememberRemoved(b *batchTx, library, collection int64) error {
	var key string
	err := b.queryRow(`SELECT key FROM item_keys WHERE item_id = ? AND kind = ?`, collection, KeyProvider).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // one the owner made
	}
	if err != nil {
		return err
	}
	_, err = b.exec(`UPDATE libraries SET options = json_set(ifnull(options, '{}'), '$.removedCollections',
		json_insert(ifnull(json(options -> '$.removedCollections'), '[]'), '$[#]', ?)) WHERE id = ?`, key, library)
	return err
}

// keepRemoved is the SQL that writes a library's options from ? while keeping what the owner
// removed from automatic collections, which the options form does not carry.
const keepRemoved = `json_set(?, '$.removedCollections', json(ifnull(options -> '$.removedCollections', '[]')),
	'$.removedFromCollections', json(ifnull(options -> '$.removedFromCollections', '{}')))`

// filmTakenOut records, or with back unrecords, that the owner took a film out of an automatic
// collection, so a refresh does not put it back. A collection the owner made, or a film with
// no provider key, has nothing to remember.
func filmTakenOut(b *batchTx, library, collection, film int64, back bool) error {
	var collectionKey, filmKey string
	err := b.queryRow(`SELECT ifnull((SELECT key FROM item_keys WHERE item_id = ?1 AND kind = ?3), ''),
		ifnull((SELECT max(key) FROM item_keys WHERE item_id = ?2 AND kind = ?3), '')`, collection, film, KeyProvider).Scan(&collectionKey, &filmKey)
	if err != nil || collectionKey == "" || filmKey == "" {
		return err
	}
	var raw sql.NullString
	if err := b.queryRow(`SELECT options FROM libraries WHERE id = ?`, library).Scan(&raw); err != nil {
		return err
	}
	options := map[string]json.RawMessage{}
	if raw.Valid {
		json.Unmarshal([]byte(raw.String), &options)
	}
	out := map[string][]string{}
	json.Unmarshal(options["removedFromCollections"], &out)
	films := slices.DeleteFunc(out[collectionKey], func(k string) bool { return k == filmKey })
	if !back {
		films = append(films, filmKey)
	}
	if len(films) == 0 {
		delete(out, collectionKey)
	} else {
		out[collectionKey] = films
	}
	options["removedFromCollections"], _ = json.Marshal(out)
	updated, err := json.Marshal(options)
	if err != nil {
		return err
	}
	_, err = b.exec(`UPDATE libraries SET options = ? WHERE id = ?`, string(updated), library)
	return err
}

// ungroupSeries is "Group films from the same series" turned off: the library's automatic
// collections nobody changed go, and one the owner renamed, reordered or edited stays as an
// ordinary collection, which nothing changes by itself from then on.
func ungroupSeries(b *batchTx, library, now int64) error {
	type automatic struct {
		id      int64
		changed bool
	}
	found, err := queryAll(context.Background(), b.tx, `SELECT i.id, (ifnull(i.locked_fields, '[]') <> '[]' OR EXISTS (SELECT 1
		FROM json_each(i.local_fields) WHERE value = 'edit')) FROM item_keys k JOIN items i ON i.id = k.item_id
		WHERE k.library_id = ? AND k.kind = ? AND i.kind = ?`, []any{library, KeyProvider, KindCollection},
		func(a *automatic) []any { return []any{&a.id, &a.changed} })
	if err != nil {
		return err
	}
	for _, a := range found {
		if a.changed {
			// The owner's version stands: it is not made again beside it if grouping is turned back on.
			if err := rememberRemoved(b, library, a.id); err != nil {
				return err
			}
			if _, err := b.exec(`DELETE FROM item_keys WHERE item_id = ? AND kind = ?`, a.id, KeyProvider); err != nil {
				return err
			}
			continue
		}
		if err := removeItem(b, library, a.id, now); err != nil {
			return err
		}
	}
	return nil
}
