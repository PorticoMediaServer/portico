package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

type Collection struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	Name      string `json:"name"`
	ItemCount int    `json:"itemCount"`
	// Revision moves with every membership or order change: an owner's
	// reorder names the revision it was made against.
	Revision int64  `json:"revision"`
	Origin   string `json:"origin"`
}

func collectionName(name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 100 {
		return "", "", requestinput.New("collection name must contain 1–100 bytes")
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return "", "", requestinput.New("collection name contains a control character")
		}
	}
	return name, strings.ToLower(strings.Join(strings.Fields(name), " ")), nil
}

const collectionColumns = `pid(e.public_id),l.library_id,e.title,c.revision,CASE WHEN EXISTS(SELECT 1 FROM catalog_automatic_collections ac WHERE ac.entity_id=e.id) THEN 'automatic' ELSE 'owner' END`

func (s *Service) Collection(id string) (Collection, error) {
	var c Collection
	err := s.read().QueryRow(`SELECT `+collectionColumns+`,c.member_count FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id JOIN catalog_libraries l ON l.id=c.library_id WHERE e.public_id=pid_blob(?) AND `+collectionHeadFence, id).Scan(&c.ID, &c.LibraryID, &c.Name, &c.Revision, &c.Origin, &c.ItemCount)
	return c, err
}

// collectionTx resolves a collection's public id to its entity and library.
func collectionTx(ctx context.Context, tx *sql.Tx, id string) (int64, int64, error) {
	var entity, library int64
	// A malformed id is nobody's collection: not found, before it reaches SQL.
	if _, ok := entityid.Decode(id); !ok {
		return 0, 0, sql.ErrNoRows
	}
	err := tx.QueryRowContext(ctx, `SELECT e.id,e.library_id FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id WHERE e.public_id=pid_blob(?) AND e.retired=0`, id).Scan(&entity, &library)
	return entity, library, err
}

func (s *Service) CreateCollection(library, name string) (Collection, error) {
	if _, err := s.movieLibrary(library); err != nil {
		return Collection{}, err
	}
	name, key, err := collectionName(name)
	if err != nil {
		return Collection{}, err
	}
	var id string
	ctx := s.Context()
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		handle, err := compactcatalog.LibraryTx(ctx, tx, library)
		if err != nil {
			return err
		}
		// A collection is made by its owner, not found in a folder: its identity
		// key is its own, so renaming one never hands its id to a new one.
		entity, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Collection, Key: compactcatalog.CollectionKey(identity.Token()), Title: name})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"library_id": handle, "name_key": key, "created_at": time.Now().UTC().Format("2006-01-02T15:04:05.000Z")}); err != nil {
			return err
		}
		id, err = entityid.Public(ctx, tx, entity)
		return err
	})
	if err != nil {
		var coder interface{ Code() int }
		if errors.As(err, &coder) && coder.Code()&255 == 19 {
			return Collection{}, ErrCollectionConflict
		}
		return Collection{}, err
	}
	return s.Collection(id)
}
func (s *Service) RenameCollection(id, name string) (Collection, error) {
	name, key, err := collectionName(name)
	if err != nil {
		return Collection{}, err
	}
	ctx := s.Context()
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		entity, _, err := collectionTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET owner_edited=1 WHERE entity_id=?`, entity); err != nil {
			return err
		}
		if err = compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Owner, map[string]any{"title": name}); err != nil {
			return err
		}
		return compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Owner, map[string]any{"name_key": key})
	})
	if err != nil {
		var coder interface{ Code() int }
		if errors.As(err, &coder) && coder.Code()&255 == 19 {
			return Collection{}, ErrCollectionConflict
		}
		return Collection{}, err
	}
	return s.Collection(id)
}
func (s *Service) DeleteCollection(id string) error {
	ctx := s.Context()
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		entity, _, err := collectionTx(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET removed=1 WHERE entity_id=?`, entity); err != nil {
			return err
		}
		return compactcatalog.DeleteEntityTx(ctx, tx, entity)
	})
}

// collectionMovieTx resolves a movie of the collection's library (0 when the
// item is not a movie of that library).
func collectionMovieTx(ctx context.Context, tx *sql.Tx, library int64, item string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND library_id=? AND kind=?`, item, library, compactcatalog.Movie).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Service) SetCollectionItem(collection, item string, present bool) error {
	ctx := s.Context()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	entity, library, err := collectionTx(ctx, tx, collection)
	if err != nil {
		return err
	}
	movie, err := collectionMovieTx(ctx, tx, library, item)
	if err != nil {
		return err
	}
	if movie == 0 {
		return sql.ErrNoRows
	}
	if err = collectionOwnerMembershipTx(ctx, tx, entity, movie, present); err != nil {
		return err
	}
	if present {
		err = appendCollectionMemberTx(ctx, tx, entity, movie)
	} else {
		err = compactcatalog.SetCollectionMemberTx(ctx, tx, entity, movie, "", false)
	}
	if err != nil {
		return err
	}
	return gated.Commit()
}

// appendCollectionMemberTx adds a title at the end of a library collection's
// own order (nothing when it is already a member: it keeps its place).
func appendCollectionMemberTx(ctx context.Context, tx *sql.Tx, collection, item int64) error {
	var member bool
	var last string
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_collection_members WHERE collection_id=? AND item_id=?),COALESCE((SELECT order_key FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? ORDER BY order_key DESC,item_id DESC LIMIT 1),'')`, collection, item, collection).Scan(&member, &last); err != nil || member {
		return err
	}
	return compactcatalog.SetCollectionMemberTx(ctx, tx, collection, item, playlistEndKey(last), true)
}

// ReorderCollection moves titles of a library collection (at most 200), the
// owner's manual order, the way a playlist's entries move: with after, in the
// order given, just after that title ("" the front); without it, they swap
// among the places they hold. Only the moved titles' keys are written. A
// collection changed since expected (its revision) is ErrPlaylistConflict.
func (s *Service) ReorderCollection(collection string, expected int64, items []string, after *string) (Collection, error) {
	if len(items) == 0 || len(items) > MaxCollectionBatch {
		return Collection{}, requestinput.New("a reorder names 1 to 200 titles")
	}
	ctx := s.Context()
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		entity, _, err := collectionTx(ctx, tx, collection)
		if err != nil {
			return err
		}
		var revision, unkeyed int64
		if err = tx.QueryRowContext(ctx, `SELECT c.revision,(SELECT count(*) FROM catalog_collection_members m WHERE m.collection_id=c.entity_id AND m.order_key='') FROM catalog_collections c WHERE c.entity_id=?`, entity).Scan(&revision, &unkeyed); err != nil {
			return err
		}
		if revision != expected {
			return ErrPlaylistConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET owner_edited=1 WHERE entity_id=?`, entity); err != nil {
			return err
		}
		if unkeyed > 0 {
			// Titles added before the collection had its own order share one
			// empty key; they get keys once, in the order they show in now.
			if _, err = tx.ExecContext(ctx, `WITH r(item_id,n) AS MATERIALIZED (SELECT item_id,ROW_NUMBER() OVER (ORDER BY order_key,item_id) FROM catalog_collection_members WHERE collection_id=?1)
 UPDATE catalog_collection_members SET order_key=(SELECT printf('%020dV',r.n) FROM r WHERE r.item_id=catalog_collection_members.item_id) WHERE collection_id=?1`, entity); err != nil {
				return err
			}
		}
		key := func(public string) (string, int64, error) {
			var k string
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT m.order_key,m.item_id FROM catalog_collection_members m JOIN catalog_entities i ON i.id=m.item_id WHERE m.collection_id=? AND i.public_id=pid_blob(?)`, entity, public).Scan(&k, &id)
			return k, id, err
		}
		seen := map[string]bool{}
		keys, ids := make([]string, 0, len(items)), make([]int64, 0, len(items))
		for _, public := range items {
			if public == "" || seen[public] {
				return requestinput.New("a reorder names distinct titles")
			}
			seen[public] = true
			k, id, err := key(public)
			if err != nil {
				return err
			}
			keys, ids = append(keys, k), append(ids, id)
		}
		if after == nil {
			sort.Strings(keys)
		} else {
			if seen[*after] {
				return requestinput.New("a title can't move after itself")
			}
			lower, upper := "", ""
			if *after != "" {
				if lower, _, err = key(*after); err != nil {
					return err
				}
			}
			raw, _ := json.Marshal(ids)
			err = tx.QueryRowContext(ctx, `SELECT order_key FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key>? AND item_id NOT IN(SELECT value FROM json_each(?)) ORDER BY order_key,item_id LIMIT 1`, entity, lower, string(raw)).Scan(&upper)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			for i := range keys {
				keys[i] = playlistRank(lower, upper, int64(i), int64(len(keys)))
			}
		}
		for i, id := range ids {
			if err = compactcatalog.SetCollectionMemberTx(ctx, tx, entity, id, keys[i], true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Collection{}, err
	}
	return s.Collection(collection)
}

// EntryOutcomes reports one membership batch item by item. Library collections
// and viewer-owned saved collections publish the same shape so a client renders
// one result view for both.
type EntryOutcome struct {
	ItemID string `json:"itemId"`
	Code   string `json:"code"`
}
type EntryOutcomes struct {
	Added     []string       `json:"added"`
	Removed   []string       `json:"removed"`
	Unchanged []string       `json:"unchanged"`
	Failed    []EntryOutcome `json:"failed"`
}
type CollectionBatch struct {
	Collection Collection    `json:"collection"`
	Entries    EntryOutcomes `json:"entries"`
}

const MaxCollectionBatch = 200

func newEntryOutcomes() EntryOutcomes {
	return EntryOutcomes{Added: []string{}, Removed: []string{}, Unchanged: []string{}, Failed: []EntryOutcome{}}
}
func (o *EntryOutcomes) fail(id, code string) {
	o.Failed = append(o.Failed, EntryOutcome{ItemID: id, Code: code})
}

// SetCollectionItems applies a library collection membership batch atomically
// in storage while reporting each item independently. An item outside the
// collection's library, of the wrong kind, or already absent is one outcome,
// not a failed request.
func (s *Service) SetCollectionItems(collection string, add, remove []string) (CollectionBatch, error) {
	out := CollectionBatch{Entries: newEntryOutcomes()}
	if len(add)+len(remove) == 0 || len(add) > MaxCollectionBatch || len(remove) > MaxCollectionBatch {
		return out, requestinput.New("a collection membership batch carries 1 to 200 additions and 200 removals")
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, add...), remove...) {
		if id == "" || len(id) > 256 || seen[id] {
			return out, requestinput.New("each batch item must name a distinct itemId")
		}
		seen[id] = true
	}
	ctx := s.Context()
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return out, err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	entity, library, err := collectionTx(ctx, tx, collection)
	if err != nil {
		return out, err
	}
	for _, item := range add {
		movie, err := collectionMovieTx(ctx, tx, library, item)
		if err != nil {
			return out, err
		}
		if movie == 0 {
			out.Entries.fail(item, "not_found")
			continue
		}
		var member bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_collection_members WHERE collection_id=? AND item_id=?)`, entity, movie).Scan(&member); err != nil {
			return out, err
		}
		if member {
			out.Entries.Unchanged = append(out.Entries.Unchanged, item)
			continue
		}
		if err = collectionOwnerMembershipTx(ctx, tx, entity, movie, true); err != nil {
			return out, err
		}
		if err = appendCollectionMemberTx(ctx, tx, entity, movie); err != nil {
			return out, err
		}
		out.Entries.Added = append(out.Entries.Added, item)
	}
	for _, item := range remove {
		movie, err := collectionMovieTx(ctx, tx, library, item)
		if err != nil {
			return out, err
		}
		var member bool
		if movie != 0 {
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_collection_members WHERE collection_id=? AND item_id=?)`, entity, movie).Scan(&member); err != nil {
				return out, err
			}
		}
		if !member {
			out.Entries.fail(item, "not_found")
			continue
		}
		if err = collectionOwnerMembershipTx(ctx, tx, entity, movie, false); err != nil {
			return out, err
		}
		if err = compactcatalog.SetCollectionMemberTx(ctx, tx, entity, movie, "", false); err != nil {
			return out, err
		}
		out.Entries.Removed = append(out.Entries.Removed, item)
	}
	if err = gated2.Commit(); err != nil {
		return out, err
	}
	out.Collection, err = s.Collection(collection)
	return out, err
}

// VisibleCollection is Collection as one viewer may see it; a hidden or unknown
// collection is sql.ErrNoRows.
// CollectionClassesReady answers ErrVisibilityBuilding while any of a
// restricted viewer's libraries is still building its restriction class, for
// every collection id alike. A hidden collection must answer exactly as an
// absent one (SEC-02), so readiness is decided before the id is looked up:
// otherwise a hidden id would answer "building" where an absent one answers
// "not found". Unrestricted viewers are always ready. One indexed probe per
// library.
func (s *Service) CollectionClassesReady(v Viewer) error {
	restrictions := v.EffectiveRestrictions()
	if !restrictions.Active() {
		return nil
	}
	for _, library := range v.Libraries {
		if _, _, err := s.collectionClassReady(library, restrictions); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) VisibleCollection(ctx context.Context, v Viewer, id string) (Collection, error) {
	if err := s.CollectionClassesReady(v); err != nil {
		return Collection{}, err
	}
	if !v.EffectiveRestrictions().Active() {
		var c Collection
		err := s.read().QueryRowContext(ctx, `SELECT `+collectionColumns+`,c.member_count FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id JOIN catalog_libraries l ON l.id=c.library_id WHERE e.public_id=pid_blob(?) AND `+collectionHeadFence, id).Scan(&c.ID, &c.LibraryID, &c.Name, &c.Revision, &c.Origin, &c.ItemCount)
		if err == nil && !v.AllowsLibrary(c.LibraryID) {
			return Collection{}, sql.ErrNoRows
		}
		return c, err
	}
	var library string
	err := s.read().QueryRowContext(ctx, `SELECT l.library_id FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id JOIN catalog_libraries l ON l.id=c.library_id WHERE e.public_id=pid_blob(?) AND `+collectionHeadFence, id).Scan(&library)
	if err != nil {
		return Collection{}, err
	}
	if !v.AllowsLibrary(library) {
		return Collection{}, sql.ErrNoRows
	}
	class, generation, err := s.collectionClassReady(library, v.EffectiveRestrictions())
	if err != nil {
		return Collection{}, err
	}
	var c Collection
	err = s.read().QueryRowContext(ctx, `SELECT `+collectionColumns+`,COALESCE(vc.total,0) FROM catalog_entities e JOIN catalog_collections c ON c.entity_id=e.id JOIN catalog_libraries l ON l.id=c.library_id LEFT JOIN catalog_collection_visible_counts vc ON vc.class_id=? AND vc.generation=? AND vc.collection_id=c.entity_id WHERE e.public_id=pid_blob(?) AND `+collectionHeadFence+` AND (c.member_count=0 OR COALESCE(vc.total,0)>0)`, class, generation, id).Scan(&c.ID, &c.LibraryID, &c.Name, &c.Revision, &c.Origin, &c.ItemCount)
	return c, err
}

// Collections lists a library's collections as the viewer may see them.
func (s *Service) Collections(library string, v Viewer, cursor string, limit int) ([]Collection, string, error) {
	if _, err := s.movieLibrary(library); err != nil {
		return nil, "", err
	}
	if !v.AllowsLibrary(library) {
		return []Collection{}, "", nil
	}
	limit = pageLimit(limit)
	scope := cursorScope{Library: library, Viewer: v.Profile + ":" + v.Fence + ":" + RestrictionFence(v.EffectiveRestrictions()), Sort: "collection_name", Direction: "asc"}
	query := `SELECT e.id,` + collectionColumns + `,c.name_key,c.member_count FROM catalog_collections c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_libraries l ON l.id=c.library_id WHERE l.library_id=? AND ` + collectionHeadFence
	args := []any{library}
	if v.EffectiveRestrictions().Active() {
		class, generation, err := s.collectionClassReady(library, v.EffectiveRestrictions())
		if err != nil {
			return nil, "", err
		}
		query = `SELECT e.id,` + collectionColumns + `,c.name_key,COALESCE(vc.total,0) FROM catalog_collections c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_libraries l ON l.id=c.library_id LEFT JOIN catalog_collection_visible_counts vc ON vc.class_id=? AND vc.generation=? AND vc.collection_id=c.entity_id WHERE l.library_id=? AND ` + collectionHeadFence + ` AND (c.member_count=0 OR COALESCE(vc.total,0)>0)`
		args = []any{class, generation, library}
	}
	expires := time.Now().Add(30 * time.Minute).Unix()
	if cursor != "" {
		c, err := s.decodeCursor(cursor, scope)
		if err != nil {
			return nil, "", err
		}
		after, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil {
			return nil, "", ErrCursor
		}
		query += ` AND (c.name_key,e.id)>(?,?)`
		args = append(args, c.Value, after)
		expires = c.Expires
	}
	query += ` ORDER BY c.name_key,e.id LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, "", err
	}
	out := []Collection{}
	keys := []string{}
	internal := []int64{}
	for rows.Next() {
		var row Collection
		var key string
		var entity int64
		if err = rows.Scan(&entity, &row.ID, &row.LibraryID, &row.Name, &row.Revision, &row.Origin, &key, &row.ItemCount); err != nil {
			rows.Close()
			return nil, "", err
		}
		out = append(out, row)
		keys = append(keys, key)
		internal = append(internal, entity)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next, err = s.encodeCursor(cursorValue{Scope: scope, Value: keys[limit-1], ID: strconv.FormatInt(internal[limit-1], 10), Expires: expires})
	}
	return out, next, err
}

// SyncAutomaticCollections applies one dirty provider set per transaction.
// Metadata publications queue normalized membership through baseline triggers.
// Reads never run this job, and no provider/network request is made here.
func (s *Service) SyncAutomaticCollections(ctx context.Context) (bool, error) {
	more := false
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var library int64
		var provider, key, name string
		var entity sql.NullInt64
		var removed bool
		err := tx.QueryRowContext(ctx, `SELECT library_id,provider,provider_id,name,entity_id,removed FROM catalog_automatic_collections WHERE dirty=1 ORDER BY library_id,provider,provider_id LIMIT 1`).Scan(&library, &provider, &key, &name, &entity, &removed)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		more = true
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET dirty=0 WHERE library_id=? AND provider=? AND provider_id=?`, library, provider, key); err != nil {
			return err
		}
		if removed {
			return nil
		}
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT group_collections FROM library_preferences p WHERE p.library_id=cl.library_id),1) FROM catalog_libraries cl WHERE cl.id=?`, library).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_automatic_members m JOIN catalog_entities e ON e.id=m.item_id WHERE m.library_id=? AND m.provider=? AND m.provider_id=? AND e.retired=0`, library, provider, key).Scan(&count); err != nil {
			return err
		}
		if !entity.Valid {
			if count < 2 {
				return nil
			}
			name = strings.TrimSpace(name)
			if strings.HasSuffix(strings.ToLower(name), " collection") {
				name = strings.TrimSpace(name[:len(name)-len(" Collection")])
			}
			if name == "" {
				return nil
			}
			nameKey := strings.ToLower(strings.Join(strings.Fields(name), " "))
			for _, letter := range name {
				if unicode.IsControl(letter) {
					return nil
				}
			}
			if len([]rune(name)) > 2048 {
				return nil
			}
			// A hand-made collection with this name is an owner's decision. Do not
			// silently take it over or change its membership.
			var occupied bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_collections WHERE library_id=? AND name_key=?)`, library, nameKey).Scan(&occupied); err != nil {
				return err
			}
			if occupied {
				return nil
			}
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Collection, Key: compactcatalog.CollectionKey("automatic:" + provider + ":" + key), Title: name})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"library_id": library, "name_key": nameKey, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
				return err
			}
			entity = sql.NullInt64{Int64: id, Valid: true}
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET entity_id=? WHERE library_id=? AND provider=? AND provider_id=?`, id, library, provider, key); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.item_id FROM catalog_automatic_members m JOIN catalog_entities e ON e.id=m.item_id WHERE m.library_id=? AND m.provider=? AND m.provider_id=? AND e.retired=0 AND NOT EXISTS(SELECT 1 FROM catalog_automatic_member_overrides o WHERE o.collection_id=? AND o.item_id=m.item_id AND o.present=0) ORDER BY e.year,e.sort_key,e.id`, library, provider, key, entity.Int64)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if e := rows.Err(); err == nil {
			err = e
		}
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err = appendCollectionMemberTx(ctx, tx, entity.Int64, id); err != nil {
				return err
			}
		}
		// Source removal removes only automatic members. Explicit owner additions
		// and an owner's order keys remain untouched.
		stale, err := tx.QueryContext(ctx, `SELECT cm.item_id FROM catalog_collection_members cm WHERE cm.collection_id=? AND NOT EXISTS(SELECT 1 FROM catalog_automatic_members m WHERE m.item_id=cm.item_id AND m.library_id=? AND m.provider=? AND m.provider_id=?) AND NOT EXISTS(SELECT 1 FROM catalog_automatic_member_overrides o WHERE o.collection_id=cm.collection_id AND o.item_id=cm.item_id AND o.present=1)`, entity.Int64, library, provider, key)
		if err != nil {
			return err
		}
		ids = nil
		for stale.Next() {
			var id int64
			if err = stale.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if e := stale.Err(); err == nil {
			err = e
		}
		stale.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err = compactcatalog.SetCollectionMemberTx(ctx, tx, entity.Int64, id, "", false); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET retired=CASE WHEN EXISTS(SELECT 1 FROM catalog_automatic_collections a WHERE a.entity_id=catalog_entities.id AND a.owner_edited=0) AND (SELECT member_count FROM catalog_collections WHERE entity_id=catalog_entities.id)<2 THEN 1 ELSE 0 END WHERE id=?`, entity.Int64); err != nil {
			return err
		}
		return compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainBrowseRows, entity.Int64)
	})
	return more, err
}

func collectionOwnerMembershipTx(ctx context.Context, tx *sql.Tx, collection, item int64, present bool) error {
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET owner_edited=1 WHERE entity_id=?`, collection); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_automatic_member_overrides(collection_id,item_id,present) SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM catalog_automatic_collections WHERE entity_id=?) ON CONFLICT(collection_id,item_id) DO UPDATE SET present=excluded.present`, collection, item, present, collection)
	return err
}

// SavedSummary is one private landing read, with each shape counted in SQL.
type SavedSummary struct {
	ServerID    string           `json:"serverId"`
	ViewerFence string           `json:"viewerFence"`
	Revision    ContentRevision  `json:"revision"`
	MyList      SavedShapeCounts `json:"myList"`
	Favorites   SavedShapeCounts `json:"favorites"`
	Playlists   int              `json:"playlists"`
	Collections int              `json:"collections"`
	SavedViews  int              `json:"savedViews"`
}

func (s *Service) SavedSummary(server string, v Viewer, a ResourceActor) (SavedSummary, error) {
	return s.savedSummary(server, v, a, nil)
}

// The HTTP caller supplies the same original-principal admission used by the
// mixed Saved page. The legacy browse-only entry never reads private refs.
func (s *Service) SavedSummaryWithReadingGroups(server string, v Viewer, a ResourceActor, admit SavedGroupAdmission) (SavedSummary, error) {
	return s.savedSummary(server, v, a, admit)
}

func (s *Service) savedSummary(server string, v Viewer, a ResourceActor, admit SavedGroupAdmission) (SavedSummary, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out SavedSummary
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).savedSummary(server, v, a, admit)
			return err
		})
		return out, err
	}
	if admit != nil {
		live, err := admit(s.Context(), s.readTxOrSnapshot())
		if err != nil {
			return SavedSummary{}, err
		}
		if live.Profile != v.Profile || live.Fence != v.Fence {
			return SavedSummary{}, identity.ErrUnauthorized
		}
		v = live
	}
	out := SavedSummary{ServerID: server, ViewerFence: v.Fence}
	var err error
	out.Revision, err = s.homeRevision(v.Libraries, v.Profile)
	if err != nil {
		return out, err
	}
	for _, view := range []string{"watchlist", "favorites"} {
		var page ContentEnvelope
		var e error
		request := ContentRequest{Viewer: v, ServerID: server, Profile: v.Profile, ViewerFence: v.Fence, View: view, Limit: 1}
		if view == "watchlist" && admit != nil {
			var mixed SavedContentEnvelope
			mixed, e = s.SavedWithReadingGroups(request, v.Libraries, admit)
			page = mixed.ContentEnvelope
		} else {
			page, e = s.Saved(request, v.Libraries)
		}
		if e != nil {
			return out, e
		}
		if page.ShapeCounts != nil {
			if view == "watchlist" {
				out.MyList = *page.ShapeCounts
			} else {
				out.Favorites = *page.ShapeCounts
			}
		}
	}
	err = s.read().QueryRow(`SELECT count(*) FROM catalog_playlists p WHERE p.deleted=0 AND ((p.owner_authority=? AND p.owner_account=? AND p.owner_profile=?) OR EXISTS(SELECT 1 FROM playlist_shares sh WHERE sh.playlist_id=p.token AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?))`, a.Authority, a.AccountID, a.ProfileID, a.Authority, a.AccountID, a.ProfileID).Scan(&out.Playlists)
	if err != nil {
		return out, err
	}
	err = s.read().QueryRow(`SELECT COALESCE(sum(r.kind='collection'),0),COALESCE(sum(r.kind='view'),0) FROM saved_resources r WHERE r.deleted=0 AND (r.owner_key=? OR (r.kind='collection' AND r.visibility='server') OR EXISTS(SELECT 1 FROM saved_resource_shares sh WHERE sh.resource_id=r.id AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?)) AND (r.kind<>'view' OR json_extract(r.definition,'$.libraryId') IN(SELECT value FROM json_each(?)))`, actorKey(a), a.Authority, a.AccountID, a.ProfileID, v.librariesJSON()).Scan(&out.Collections, &out.SavedViews)
	return out, err
}

type SavedResourceCard struct {
	SavedResource
	Shape             string         `json:"shape"`
	ArtworkPaths      []string       `json:"artworkPaths"`
	Samples           []ContentEntry `json:"samples"`
	OnHome            bool           `json:"onHome"`
	TotalIsLowerBound bool           `json:"totalIsLowerBound,omitempty"`
}
type SavedResourceCardsPage struct {
	ServerID    string              `json:"serverId"`
	ViewerFence string              `json:"viewerFence"`
	Revision    int64               `json:"revision"`
	Resources   []SavedResourceCard `json:"resources"`
	ShapeCounts SavedShapeCounts    `json:"shapeCounts"`
	NextCursor  string              `json:"nextCursor"`
}

// SavedResourcesForViewer keeps the underlying continuation even for an empty
// filtered window. A short/empty window never invents exhaustion. The shape is
// part of the signed cursor's authority scope; it cannot cross shape filters.
func (s *Service) SavedResourcesForViewer(server string, v Viewer, a ResourceActor, kind, cursor string, pinned bool, limit int, shape string, homeRows []string) (SavedResourceCardsPage, error) {
	out := SavedResourceCardsPage{ServerID: server, ViewerFence: v.Fence, Resources: []SavedResourceCard{}}
	if shape != "" && shape != "posters" && shape != "squares" {
		return out, ErrCursor
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var result SavedResourceCardsPage
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			result, err = s.WithContext(ctx).SavedResourcesForViewer(server, v, a, kind, cursor, pinned, limit, shape, homeRows)
			return err
		})
		return result, err
	}
	page, err := s.SavedResources(server, v.Fence+":shape="+shape, a, kind, cursor, pinned, limit)
	if err != nil {
		return out, err
	}
	out.Revision, out.NextCursor = page.Revision, page.NextCursor
	visible, visibleArgs := v.itemVisibilitySQL("e.id")
	for _, resource := range page.Resources {
		resource.ViewerFence = v.Fence
		card := SavedResourceCard{SavedResource: resource, Shape: "posters", ArtworkPaths: []string{}, Samples: []ContentEntry{}, OnHome: containsString(homeRows, homeViewPrefix+resource.ID)}
		ids := []string{}
		if resource.Kind == "view" && resource.Definition != nil && resource.Status == "ready" {
			definition := resource.Definition
			if !v.AllowsLibrary(definition.LibraryID) {
				continue
			}
			library, e := s.library(definition.LibraryID)
			if e != nil {
				return out, e
			}
			if homeArtwork(library.Kind) == "square" {
				card.Shape = "squares"
			}
			preview, e := s.BrowseEntities(v, BrowseRequest{ServerID: server, Library: definition.LibraryID, Pivot: definition.Pivot, Query: definition.Query, Sort: definition.Sort, Limit: 4})
			if e != nil {
				return out, e
			}
			card.EntryCount = preview.PageInfo.Total
			card.TotalIsLowerBound = preview.PageInfo.TotalIsLowerBound
			card.Samples = preview.Entries
		} else if resource.Kind == "collection" {
			if err = s.read().QueryRow(`SELECT count(*) FROM saved_resource_entries member JOIN catalog_entities e ON e.id=member.item_id WHERE member.resource_id=? AND `+visible, append([]any{resource.ID}, visibleArgs...)...).Scan(&card.EntryCount); err != nil {
				return out, err
			}
			query := `SELECT pid(e.public_id),e.kind FROM saved_resource_entries member JOIN catalog_entities e ON e.id=member.item_id WHERE member.resource_id=? AND ` + visible + ` ORDER BY member.order_key,member.id LIMIT 4`
			rows, e := s.read().Query(query, append([]any{resource.ID}, visibleArgs...)...)
			if e != nil {
				return out, e
			}
			var firstKind int
			for rows.Next() {
				var id string
				var k int
				if e = rows.Scan(&id, &k); e != nil {
					break
				}
				if firstKind == 0 {
					firstKind = k
				}
				ids = append(ids, id)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return out, e
			}
			if firstKind == 5 || firstKind == 6 || firstKind == 7 || firstKind == 8 || firstKind == 9 || firstKind == 13 {
				card.Shape = "squares"
			}
			card.Samples, err = s.WithRecommendationRestrictions(v.Restrictions).homeEngineEntries(v.Profile, ids)
			if err != nil {
				return out, err
			}
		}
		for _, entry := range card.Samples {
			if entry.PosterURL != "" {
				card.ArtworkPaths = append(card.ArtworkPaths, entry.PosterURL)
			}
		}
		if shape == "" || shape == card.Shape {
			out.Resources = append(out.Resources, card)
		}
	}
	// Counts use the same server-owned shape (view's library, collection's first
	// currently permitted member) rather than filtering a client-loaded list.
	allowed, args := v.itemVisibilitySQL("e.id")
	shapeSQL := `CASE WHEN r.kind='view' THEN COALESCE((SELECT CASE WHEN l.kind IN('music','audiobook') THEN 'squares' ELSE 'posters' END FROM libraries l WHERE l.id=json_extract(r.definition,'$.libraryId')),'posters') ELSE COALESCE((SELECT CASE WHEN e.kind IN(5,6,7,8,9,13) THEN 'squares' ELSE 'posters' END FROM saved_resource_entries member JOIN catalog_entities e ON e.id=member.item_id WHERE member.resource_id=r.id AND ` + allowed + ` ORDER BY member.order_key,member.id LIMIT 1),'posters') END`
	base := ` FROM saved_resources r WHERE r.deleted=0 AND r.kind=? AND (r.owner_key=? OR (r.kind='collection' AND r.visibility='server') OR EXISTS(SELECT 1 FROM saved_resource_shares sh WHERE sh.resource_id=r.id AND sh.authority=? AND sh.account_id=? AND sh.profile_id=?)) AND (r.kind<>'view' OR json_extract(r.definition,'$.libraryId') IN(SELECT value FROM json_each(?)))`
	bound := append(append([]any{}, args...), kind, actorKey(a), a.Authority, a.AccountID, a.ProfileID, v.librariesJSON())
	if pinned {
		base += ` AND EXISTS(SELECT 1 FROM saved_pins pin WHERE pin.owner_key=? AND pin.kind=r.kind AND pin.resource_id=r.id AND pin.pinned=1)`
		bound = append(bound, actorKey(a))
	}
	err = s.read().QueryRow(`SELECT COALESCE(sum(shape='posters'),0),COALESCE(sum(shape='squares'),0) FROM (SELECT `+shapeSQL+` AS shape`+base+`)`, bound...).Scan(&out.ShapeCounts.Posters, &out.ShapeCounts.Squares)
	return out, err
}

// ProjectCoverSamples resolves only returned containers. Four authorized cover
// references per tile prevent client per-card browse requests. Artist circles
// use real album art; authors use book covers, never fabricated portraits.
func (s *Service) ProjectCoverSamples(profile string, entries []ContentEntry) error {
	ids := []string{}
	for _, entry := range entries {
		switch entry.Kind {
		case "artist", "author", "collection":
			ids = append(ids, entry.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	restriction, args := ItemRestrictionSQL("member.item_id", s.recRestrictions)
	query := `SELECT pid(parent.public_id),COALESCE((SELECT json_group_array(json_object('id',id,'kind',kind)) FROM (
 SELECT DISTINCT CASE part.kind WHEN 7 THEN (SELECT pid(a.public_id) FROM catalog_songs song JOIN catalog_entities a ON a.id=song.album_id WHERE song.entity_id=part.id) WHEN 9 THEN (SELECT pid(b.public_id) FROM catalog_book_files file JOIN catalog_entities b ON b.id=file.book_id WHERE file.entity_id=part.id) ELSE pid(part.public_id) END AS id,
 CASE part.kind WHEN 7 THEN 'album' WHEN 9 THEN 'book' ELSE 'item' END AS kind
 FROM catalog_browse_memberships member JOIN catalog_entities part ON part.id=member.item_id WHERE member.entity_id=parent.id AND part.library_id=parent.library_id AND part.retired=0 AND ` + restriction + recordingsClause("member.item_id", s.recRestrictions) + ` ORDER BY member.item_id LIMIT 4)), '[]')
 FROM json_each(?) requested JOIN catalog_entities parent ON parent.public_id=pid_blob(requested.value)`
	rows, err := s.read().Query(query, append(args, idsJSON(ids))...)
	if err != nil {
		return err
	}
	samples := map[string][]artworkTarget{}
	targets := []artworkTarget{}
	for rows.Next() {
		var parent, raw string
		if err = rows.Scan(&parent, &raw); err != nil {
			break
		}
		var paths []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		}
		if err = json.Unmarshal([]byte(raw), &paths); err != nil {
			break
		}
		for _, path := range paths {
			if path.ID != "" {
				target := artworkTarget{path.Kind, path.ID}
				samples[parent] = append(samples[parent], target)
				targets = append(targets, target)
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	art, err := s.resolveArtwork(targets)
	if err != nil {
		return err
	}
	sampleIDs := []string{}
	for _, target := range targets {
		if !containsString(sampleIDs, target.ID) {
			sampleIDs = append(sampleIDs, target.ID)
		}
	}
	sampleEntries, err := s.homeEngineEntries(profile, sampleIDs)
	if err != nil {
		return err
	}
	byID := map[string]ContentEntry{}
	for _, entry := range sampleEntries {
		byID[entry.ID] = entry
	}
	for _, target := range targets {
		value := art[target]
		if value.PosterURL == "" {
			value.PosterURL = byID[target.ID].PosterURL
		}
		if value.BackdropURL == "" {
			value.BackdropURL = byID[target.ID].BackdropURL
		}
		art[target] = value
	}
	for i := range entries {
		entry := &entries[i]
		for _, target := range samples[entry.ID] {
			if url := art[target].PosterURL; url != "" && !containsString(entry.ArtworkPaths, url) {
				entry.ArtworkPaths = append(entry.ArtworkPaths, url)
			}
		}
		if entry.Kind != "collection" {
			continue
		}
		var first, last sql.NullInt64
		var duration float64
		var watched int64
		restriction, args := ItemRestrictionSQL("member.item_id", s.recRestrictions)
		query := `SELECT min(NULLIF(movie.year,0)),max(NULLIF(movie.year,0)),COALESCE(sum(metric.duration),0),COALESCE(sum(` + personalstate.CompactSQL("?", "member.item_id") + `),0)
 FROM catalog_collection_members member JOIN catalog_entities movie ON movie.id=member.item_id LEFT JOIN catalog_browse_item_metrics metric ON metric.item_id=member.item_id
 WHERE member.collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND movie.retired=0 AND ` + restriction + recordingsClause("member.item_id", s.recRestrictions)
		if err = s.read().QueryRow(query, append([]any{profile, entry.ID}, args...)...).Scan(&first, &last, &duration, &watched); err != nil {
			return err
		}
		if first.Valid {
			year := int(first.Int64)
			entry.YearMin = &year
		}
		if last.Valid {
			year := int(last.Int64)
			entry.YearMax = &year
		}
		entry.Duration = &duration
		entry.WatchedCount = &watched
		if entry.BackdropURL == "" {
			for _, target := range samples[entry.ID] {
				if url := art[target].BackdropURL; url != "" {
					entry.BackdropURL = url
					break
				}
			}
		}
		if err = s.read().QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM catalog_automatic_collections automatic JOIN catalog_entities e ON e.id=automatic.entity_id WHERE e.public_id=pid_blob(?)) THEN 'automatic' ELSE 'owner' END`, entry.ID).Scan(&entry.Origin); err != nil {
			return err
		}
	}
	return nil
}

// ProjectPlaylistSamples decorates a returned directory page, with the current
// viewer's permitted entries only. The client receives four real cover paths.
func (s *Service) ProjectPlaylistSamples(v Viewer, entries []ContentEntry) error {
	visible, args := v.itemVisibilitySQL("part.id")
	for i := range entries {
		entry := &entries[i]
		if entry.Kind != "playlist" {
			continue
		}
		rows, err := s.read().Query(`SELECT pid(part.public_id),COALESCE(detail.poster_url,'') FROM catalog_playlists p JOIN catalog_playlist_entries member ON member.playlist_id=p.id JOIN catalog_entities part ON part.id=member.item_id LEFT JOIN catalog_item_details detail ON detail.entity_id=part.id WHERE p.token=? AND part.retired=0 AND `+visible+` ORDER BY member.order_key,member.id LIMIT 4`, append([]any{entry.ID}, args...)...)
		if err != nil {
			return err
		}
		targets := []artworkTarget{}
		legacy := map[string]string{}
		for rows.Next() {
			var id, poster string
			if err = rows.Scan(&id, &poster); err != nil {
				break
			}
			targets = append(targets, artworkTarget{Kind: "item", ID: id})
			if poster != "" {
				legacy[id] = "/v1/items/" + id + "/art/poster"
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err == nil {
			err = describeItemArtMap(s, legacy)
		}
		if err != nil {
			return err
		}
		// Directory art does not require a published playable-page projection.
		// A permitted entry awaiting ingestion or without artwork stays readable.
		art, err := s.resolveArtwork(targets)
		if err != nil {
			return err
		}
		for _, target := range targets {
			poster := art[target].PosterURL
			if poster == "" {
				poster = legacy[target.ID]
			}
			if poster != "" {
				entry.ArtworkPaths = append(entry.ArtworkPaths, poster)
			}
		}
	}
	return nil
}
