package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func catalogMutationKey(n int) string {
	return fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), n)
}
func catalogMutationContext(key, operation string, input any) context.Context {
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(raw)
	return usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), usermutation.Binding{Authority: "local", AccountID: "account", ProfileID: "p", FamilyID: "family"}, usermutation.Envelope{Key: key, Operation: operation, Digest: hex.EncodeToString(digest[:])})
}
func seedCatalogMutationAccount(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'00','p')`); err != nil {
		t.Fatal(err)
	}
}
func assertTinyCatalogReceipts(t *testing.T, db *sql.DB, expected int) {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM user_mutation_receipts`).Scan(&n); err != nil || n != expected {
		t.Fatalf("tiny receipts=%d want=%d: %v", n, expected, err)
	}
	for _, table := range []string{"playlist_receipts", "personal_receipts", "personal_batch_receipts", "saved_resource_receipts", "reading_saved_group_receipts", "saved_operation_fences"} {
		if err := db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE instr(operation_id,'p251_')>0`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("legacy %s contains %d protocol payloads/fences: %v", table, n, err)
		}
	}
}

func TestCatalogMutationPlaylistConcurrentReplayDeleteAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, _, s := phase34PlaylistDB(t, path)
	defer func() { db.Close() }()
	seedCatalogMutationAccount(t, db)
	actor := ResourceActor{"local", "account", "p"}
	name := "Do not retain this response name"
	m := PlaylistMutation{OperationID: catalogMutationKey(1), Name: &name}
	ctx := catalogMutationContext(m.OperationID, "post_playlists", m)
	ctx = usermutation.WithAuthority(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var synchronous int
		if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
			return err
		}
		if synchronous != 2 {
			return fmt.Errorf("catalog lost FULL user durability: synchronous=%d", synchronous)
		}
		return nil
	})
	var wg sync.WaitGroup
	ids := make(chan string, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.WithContext(ctx).MutatePlaylist(actor, "", "create", "", m, allow)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- out.PlaylistID
		}()
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id != "" && got != id {
			t.Fatalf("duplicate create: %s != %s", got, id)
		}
		id = got
	}
	if id == "" {
		t.Fatal("no accepted create")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_playlists`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("playlists=%d %v", count, err)
	}
	if _, err := s.WithContext(usermutation.WithAuthority(ctx, func(context.Context, *sql.Tx) error { return identity.ErrUnauthorized })).MutatePlaylist(actor, "", "create", "", m, allow); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("replay authority: %v", err)
	}
	changed := m
	other := "Different"
	changed.Name = &other
	if _, err := s.WithContext(catalogMutationContext(m.OperationID, "post_playlists", changed)).MutatePlaylist(actor, "", "create", "", changed, allow); !errors.Is(err, usermutation.ErrConflict) {
		t.Fatalf("changed digest: %v", err)
	}
	deletion := PlaylistMutation{OperationID: catalogMutationKey(2), ExpectedRevision: 1}
	deleteCtx := catalogMutationContext(deletion.OperationID, "delete_playlists_id", deletion)
	removed, err := s.WithContext(deleteCtx).MutatePlaylist(actor, id, "delete", "", deletion, allow)
	if err != nil || !removed.Deleted || removed.Revision != 2 {
		t.Fatalf("delete %+v %v", removed, err)
	}
	if _, err = db.Exec(`DELETE FROM catalog_playlists WHERE token=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s = New(db)
	replay, err := s.WithContext(deleteCtx).MutatePlaylist(actor, id, "delete", "", deletion, allow)
	if err != nil || !replay.Deleted || replay.PlaylistID != id || replay.Revision != 2 {
		t.Fatalf("removed target replay %+v %v", replay, err)
	}
	created, err := s.WithContext(ctx).MutatePlaylist(actor, "", "create", "", m, allow)
	if err != nil || created.PlaylistID != id || !created.Deleted {
		t.Fatalf("create replay after deletion %+v %v", created, err)
	}
	assertTinyCatalogReceipts(t, db, 2)
}

func TestCatalogMutationPlaylistAllEditsKeepTinyReceipts(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	actor := ResourceActor{"local", "account", "p"}
	name := "Playlist"
	next := 10
	apply := func(id, action, target string, m PlaylistMutation) PlaylistReceipt {
		t.Helper()
		m.OperationID = catalogMutationKey(next)
		next++
		ctx := catalogMutationContext(m.OperationID, action, struct {
			ID, Target string
			M          PlaylistMutation
		}{id, target, m})
		out, err := s.WithContext(ctx).MutatePlaylist(actor, id, action, target, m, allow)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		replay, err := s.WithContext(ctx).MutatePlaylist(actor, id, action, target, m, allow)
		if err != nil || replay.PlaylistID != out.PlaylistID || replay.Revision != out.Revision {
			t.Fatalf("%s replay %+v %v", action, replay, err)
		}
		return out
	}
	out := apply("", "create", "", PlaylistMutation{Name: &name, ItemIDs: []string{names["a"].Public, names["b"].Public}})
	id := out.PlaylistID
	out = apply(id, "add", "", PlaylistMutation{ExpectedRevision: out.Revision, ItemID: names["c"].Public})
	entries := playlistOrder(t, s, id)
	out = apply(id, "reorder", "", PlaylistMutation{ExpectedRevision: out.Revision, EntryIDs: []string{entries[2], entries[0], entries[1]}})
	out = apply(id, "remove", entries[1], PlaylistMutation{ExpectedRevision: out.Revision})
	out = apply(id, "update", "", PlaylistMutation{ExpectedRevision: out.Revision, Name: &name})
	share := PlaylistMutation{ExpectedRevision: out.Revision, Authority: "local", AccountID: "other", ProfileID: "other", Role: "editor"}
	out = apply(id, "share", "", share)
	share.ExpectedRevision = out.Revision
	share.Role = ""
	out = apply(id, "unshare", "", share)
	apply(id, "delete", "", PlaylistMutation{ExpectedRevision: out.Revision})
	assertTinyCatalogReceipts(t, db, 8)
}

func TestCatalogMutationPersonalAndPartialBatch(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	yes, no := true, false
	m := PersonalMutation{OperationID: catalogMutationKey(30), Favorite: &yes}
	ctx := catalogMutationContext(m.OperationID, "put_items_id_personal_state", m)
	first, err := s.WithContext(ctx).SetPersonal("local:account", "p", names["a"].Public, m, allow)
	if err != nil || !first.Favorite {
		t.Fatalf("personal %+v %v", first, err)
	}
	later, err := s.SetPersonal("local:account", "p", names["a"].Public, PersonalMutation{OperationID: "later", ExpectedRevision: first.Revision, Favorite: &no}, allow)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.WithContext(ctx).SetPersonal("local:account", "p", names["a"].Public, m, allow)
	if err != nil || replay.Favorite || replay.Revision != later.Revision {
		t.Fatalf("must reread current state %+v %v", replay, err)
	}
	stale := int64(99)
	batch := PersonalBatchMutation{OperationID: catalogMutationKey(31), Items: []PersonalBatchItem{{ItemID: names["a"].Public, ExpectedRevision: &stale, Favorite: &yes}, {ItemID: names["b"].Public, Favorite: &yes, Watchlisted: &yes}, {ItemID: identity.Token(), Favorite: &yes}}}
	batchCtx := catalogMutationContext(batch.OperationID, "put_items_personal_state_batch", batch)
	result, err := s.WithContext(batchCtx).SetPersonalBatch("local:account", "p", batch, nil)
	if err != nil || result.Updated != 1 || result.Failed != 2 {
		t.Fatalf("partial batch %+v %v", result, err)
	}
	current, err := s.Personal("p", names["b"].Public)
	if err != nil || !current.Favorite || !current.Watchlisted || current.Revision != 2 {
		t.Fatalf("multiple fields %+v %v", current, err)
	}
	result, err = s.WithContext(batchCtx).SetPersonalBatch("local:account", "p", batch, nil)
	if err != nil || len(result.Results) != 0 {
		t.Fatalf("compact replay %+v %v", result, err)
	}
	after, _ := s.Personal("p", names["b"].Public)
	if after.Revision != current.Revision {
		t.Fatalf("batch repeated %+v", after)
	}
	assertTinyCatalogReceipts(t, db, 2)
}

func TestCatalogMutationReceiptFailureRollsBackBatchPrefix(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	if _, err := db.Exec(`CREATE TRIGGER reject_catalog_receipt BEFORE UPDATE OF status ON user_mutation_receipts WHEN NEW.status<>'reserved' BEGIN SELECT RAISE(ABORT,'receipt storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	yes := true
	batch := PersonalBatchMutation{OperationID: catalogMutationKey(40), Items: []PersonalBatchItem{{ItemID: names["a"].Public, Favorite: &yes}, {ItemID: names["b"].Public, Watchlisted: &yes}}}
	if _, err := s.WithContext(catalogMutationContext(batch.OperationID, "put_items_personal_state_batch", batch)).SetPersonalBatch("local:account", "p", batch, nil); err == nil || !strings.Contains(err.Error(), "receipt storage failed") {
		t.Fatalf("storage failure %v", err)
	}
	for _, name := range []string{"a", "b"} {
		out, err := s.Personal("p", names[name].Public)
		if err != nil || out.Revision != 0 {
			t.Fatalf("prefix committed %+v %v", out, err)
		}
	}
	assertTinyCatalogReceipts(t, db, 0)
}

func TestCatalogMutationSavedResourcesPinsAndOrder(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	actor := ResourceActor{"local", "account", "p"}
	name := "Saved"
	next := 50
	apply := func(id, action string, m SavedResourceMutation) SavedResourceReceipt {
		t.Helper()
		m.OperationID = catalogMutationKey(next)
		next++
		ctx := catalogMutationContext(m.OperationID, action, struct {
			ID string
			M  SavedResourceMutation
		}{id, m})
		out, err := s.WithContext(ctx).MutateSavedResource(actor, id, action, m, allow)
		if err != nil {
			t.Fatalf("%s %+v %v", action, out, err)
		}
		replay, err := s.WithContext(ctx).MutateSavedResource(actor, id, action, m, allow)
		if err != nil || replay.ResourceID != out.ResourceID || replay.Revision != out.Revision || replay.Entries != nil {
			t.Fatalf("%s compact replay %+v %v", action, replay, err)
		}
		return out
	}
	out := apply("", "create", SavedResourceMutation{Kind: "collection", Name: &name})
	id := out.ResourceID
	out = apply(id, "entries", SavedResourceMutation{ExpectedRevision: out.Revision, AddItemIDs: []string{names["a"].Public, identity.Token(), names["b"].Public}})
	if out.Entries == nil || len(out.Entries.Added) != 2 || len(out.Entries.Failed) != 1 {
		t.Fatalf("membership partial outcomes %+v", out)
	}
	var entries []string
	rows, err := db.Query(`SELECT id FROM saved_resource_entries WHERE resource_id=? ORDER BY order_key`, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var entry string
		if err = rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	out = apply(id, "reorder", SavedResourceMutation{ExpectedRevision: out.Revision, EntryIDs: []string{entries[1], entries[0]}})
	out = apply(id, "update", SavedResourceMutation{ExpectedRevision: out.Revision, Name: &name})
	share := SavedResourceMutation{ExpectedRevision: out.Revision, Authority: "local", AccountID: "other", ProfileID: "other", Role: "viewer"}
	out = apply(id, "share", share)
	share.ExpectedRevision = out.Revision
	share.Role = ""
	out = apply(id, "unshare", share)
	pin := PinMutation{OperationID: catalogMutationKey(70), Pinned: true}
	pinCtx := catalogMutationContext(pin.OperationID, "put_saved_pins_kind_id", pin)
	firstPin, err := s.WithContext(pinCtx).MutateSavedPin(actor, "collection", id, pin, allow)
	if err != nil || !firstPin.Pinned {
		t.Fatalf("pin %+v %v", firstPin, err)
	}
	orderCtx := catalogMutationContext(catalogMutationKey(71), "put_saved_pins_order", id)
	order := []PinOrderEntry{{Kind: "collection", ID: id}}
	arranged, err := s.WithContext(orderCtx).SetPinOrder(actor, 0, order, allow)
	if err != nil || arranged.Revision != 1 {
		t.Fatalf("order %+v %v", arranged, err)
	}
	replayOrder, err := s.WithContext(orderCtx).SetPinOrder(actor, 0, order, allow)
	if err != nil || replayOrder.Revision != 1 || len(replayOrder.Order) != 1 {
		t.Fatalf("order replay %+v %v", replayOrder, err)
	}
	pinReplay, err := s.WithContext(pinCtx).MutateSavedPin(actor, "collection", id, pin, allow)
	if err != nil || pinReplay.PinRevision != 1 || !pinReplay.Pinned {
		t.Fatalf("pin replay %+v %v", pinReplay, err)
	}
	apply(id, "delete", SavedResourceMutation{ExpectedRevision: out.Revision})
	assertTinyCatalogReceipts(t, db, 9)
}

func TestCatalogMutationContainerMarkAndUndoReplay(t *testing.T) {
	s, scheduler, p, _, access, _, names := bulkFixture(t, 2)
	seedCatalogMutationAccount(t, s.db)
	yes := true
	id := names["show"].Public
	m := ContainerPersonalMutation{Watched: &yes}
	ctx := catalogMutationContext(catalogMutationKey(80), "set_container_personal_state", m)
	first, err := s.SetContainerPersonal(ctx, p, "show", id, m, scheduler, access)
	if err != nil || first.Revision != 1 {
		t.Fatalf("container %+v %v", first, err)
	}
	replay, err := s.SetContainerPersonal(ctx, p, "show", id, m, scheduler, access)
	if err != nil || replay.Revision != 1 {
		t.Fatalf("container replay %+v %v", replay, err)
	}
	undo := ContainerUndoMutation{ExpectedRevision: first.Revision, UndoToken: first.UndoToken}
	undoCtx := catalogMutationContext(catalogMutationKey(81), "undo_container_personal_state", undo)
	restored, err := s.UndoContainerPersonal(undoCtx, p, "show", id, undo, access)
	if err != nil || restored.Revision != 2 {
		t.Fatalf("undo %+v %v", restored, err)
	}
	replay, err = s.UndoContainerPersonal(undoCtx, p, "show", id, undo, access)
	if err != nil || replay.Revision != 2 {
		t.Fatalf("spent undo replay %+v %v", replay, err)
	}
	replay, err = s.SetContainerPersonal(ctx, p, "show", id, m, scheduler, access)
	if err != nil || replay.Revision != 2 || replay.Watched {
		t.Fatalf("current container replay %+v %v", replay, err)
	}
	assertTinyCatalogReceipts(t, s.db, 2)
}

func TestCatalogMutationReadingSavedGroupAndRemovedReferenceReplay(t *testing.T) {
	db, s, _ := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "p", Role: "owner", ServerID: "server"}}
	target := ReadingSavedGroupTarget{LibraryID: "reading", GroupKind: ReadingSavedSeries, SeriesID: strings.Repeat("s", 43)}
	proof := ReadingSavedGroupProof{Key: "Accepted private group key", ViewerFence: strings.Repeat("f", 64)}
	present := true
	resolver := func(context.Context, *sql.Tx, identity.Principal, ReadingSavedGroupTarget) (ReadingSavedGroupProof, error) {
		if !present {
			return ReadingSavedGroupProof{}, sql.ErrNoRows
		}
		return proof, nil
	}
	operation := catalogMutationKey(90)
	ctx := catalogMutationContext(operation, "put_reading_group_my_list", target)
	state, _, err := s.WithContext(ctx).SetReadingSavedGroup(p, "server", target, operation, 0, true, resolver)
	if err != nil || state.ReferenceID == "" || state.Revision != 1 || !state.Listed {
		t.Fatalf("set group %+v %v", state, err)
	}
	present = false
	replay, _, err := s.WithContext(ctx).SetReadingSavedGroup(p, "server", target, operation, 0, true, resolver)
	if err != nil || replay.ReferenceID != state.ReferenceID || !replay.Listed || replay.Revision != 1 {
		t.Fatalf("absent group replay %+v %v", replay, err)
	}
	removal := catalogMutationKey(91)
	removeCtx := catalogMutationContext(removal, "delete_reading_saved_group_reference", state.ReferenceID)
	authorize := func(context.Context, *sql.Tx, identity.Principal) error { return nil }
	removed, err := s.WithContext(removeCtx).RemoveReadingSavedReference(p, "server", state.ReferenceID, removal, 1, authorize)
	if err != nil || !removed.Removed || removed.Revision != 2 {
		t.Fatalf("remove %+v %v", removed, err)
	}
	if _, err = db.Exec(`DELETE FROM reading_saved_groups WHERE reference_id=?`, state.ReferenceID); err != nil {
		t.Fatal(err)
	}
	removed, err = s.WithContext(removeCtx).RemoveReadingSavedReference(p, "server", state.ReferenceID, removal, 1, authorize)
	if err != nil || !removed.Removed || removed.Revision != 2 {
		t.Fatalf("missing reference replay %+v %v", removed, err)
	}
	assertTinyCatalogReceipts(t, db, 2)
}

func TestCatalogMutationExpiredKeyCannotReachTargetCallback(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	yes := true
	key := fmt.Sprintf("p251_%013d_%032x", time.Now().Add(-31*24*time.Hour).UnixMilli(), 100)
	m := PersonalMutation{OperationID: key, Favorite: &yes}
	called := false
	_, err := s.WithContext(catalogMutationContext(key, "put_items_id_personal_state", m)).SetPersonal("local:account", "p", names["a"].Public, m, func(*sql.Tx) error { called = true; return nil })
	if !errors.Is(err, usermutation.ErrExpired) || called {
		t.Fatalf("expiry before targets: called=%v err=%v", called, err)
	}
	current, err := s.Personal("p", names["a"].Public)
	if err != nil || current.Revision != 0 {
		t.Fatalf("expired key changed state %+v %v", current, err)
	}
	assertTinyCatalogReceipts(t, db, 0)
}

func TestCatalogMutationReadingPersonalAndMarkAll(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	seedCatalogMutationAccount(t, c.DB)
	c.Drain()
	viewer.Profile = "p"
	viewer.Fence = strings.Repeat("a", 64)
	markKey := catalogMutationKey(110)
	markCtx := catalogMutationContext(markKey, "put_items_itemId_reading_mark", items[0].Public)
	mark, err := s.WithContext(markCtx).SetReadingMark("local:account", "p", items[0].Public, markKey, 0, true, allow)
	if err != nil || !mark.Read || mark.Revision != 1 {
		t.Fatalf("reading mark %+v %v", mark, err)
	}
	favoriteKey := catalogMutationKey(111)
	favoriteCtx := catalogMutationContext(favoriteKey, "put_items_itemId_reading_favorite", items[0].Public)
	favorite, err := s.WithContext(favoriteCtx).SetReadingFavorite("local:account", "p", items[0].Public, favoriteKey, 1, true, allow)
	if err != nil || !favorite.Favorite || favorite.Revision != 2 {
		t.Fatalf("reading favorite %+v %v", favorite, err)
	}
	savedKey := catalogMutationKey(112)
	savedCtx := catalogMutationContext(savedKey, "put_items_itemId_reading_my_list", items[0].Public)
	saved, err := s.WithContext(savedCtx).SetReadingSaved("local:account", "p", items[0].Public, savedKey, 2, true, allow)
	if err != nil || !saved.Listed || saved.Revision != 3 {
		t.Fatalf("reading saved %+v %v", saved, err)
	}
	mark, err = s.WithContext(markCtx).SetReadingMark("local:account", "p", items[0].Public, markKey, 0, true, allow)
	if err != nil || mark.Revision != 3 || !mark.Read {
		t.Fatalf("current reading mark %+v %v", mark, err)
	}
	authorize := func(context.Context, *sql.Tx) (Viewer, error) { return viewer, nil }
	projection, err := s.ReadingMarkAllProjection("server", "reading", authorize)
	if err != nil {
		t.Fatal(err)
	}
	in := ReadingMarkAllCommand{OperationID: catalogMutationKey(113), ViewerFence: viewer.Fence, ExpectedRevision: projection.Revision}
	ctx := catalogMutationContext(in.OperationID, "post_libraries_libraryId_reading_mark_all", in)
	all, err := s.WithContext(ctx).SetReadingMarkAll("server", "reading", in, authorize)
	if err != nil || all.Count != 4 || all.Changed != 3 {
		t.Fatalf("mark all %+v %v", all, err)
	}
	replay, err := s.WithContext(ctx).SetReadingMarkAll("server", "reading", in, authorize)
	if err != nil || replay.Count != 4 || replay.Changed != 0 || replay.Revision != all.Revision {
		t.Fatalf("mark all current replay %+v %v", replay, err)
	}
	for _, item := range items {
		current, err := s.Personal("p", item.Public)
		if err != nil || !current.Watched {
			t.Fatalf("mark all effect %+v %v", current, err)
		}
	}
	assertTinyCatalogReceipts(t, c.DB, 4)
}

func TestCatalogMutationReservedKeyCannotUseLegacyReceiptPath(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	seedCatalogMutationAccount(t, db)
	yes := true
	key := catalogMutationKey(120)
	actor := ResourceActor{"local", "account", "p"}
	name := "Reserved"
	checks := []func() error{
		func() error {
			_, err := s.SetPersonal("local:account", "p", names["a"].Public, PersonalMutation{OperationID: key, Favorite: &yes}, allow)
			return err
		},
		func() error {
			_, err := s.SetPersonalBatch("local:account", "p", PersonalBatchMutation{OperationID: key, Items: []PersonalBatchItem{{ItemID: names["a"].Public, Favorite: &yes}}}, nil)
			return err
		},
		func() error {
			_, err := s.MutatePlaylist(actor, "", "create", "", PlaylistMutation{OperationID: key, Name: &name}, allow)
			return err
		},
		func() error {
			_, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: key, Kind: "collection", Name: &name}, allow)
			return err
		},
	}
	for _, check := range checks {
		if err := check(); !errors.Is(err, usermutation.ErrInvalidKey) {
			t.Fatalf("reserved namespace admitted: %v", err)
		}
	}
	current, err := s.Personal("p", names["a"].Public)
	if err != nil || current.Revision != 0 {
		t.Fatalf("reserved key effects %+v %v", current, err)
	}
	assertTinyCatalogReceipts(t, db, 0)
}
