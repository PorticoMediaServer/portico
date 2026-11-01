package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestHomeReadingSavedViewAndPickerProjection(t *testing.T) {
	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			c, s, viewer, items := readingBrowseFixture(t, kind)
			movies := c.Library("movies", "Movies", "movie", t.TempDir())
			movie := c.Movie(movies, "/movie.mkv", "Movie", 2020)
			viewer.Libraries = append(viewer.Libraries, "movies")
			c.Drain()
			actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
			viewer.Profile = actorKey(actor)
			name := "Reading view"
			pivot := "books"
			if kind == "comic" {
				pivot = "comics"
			}
			definition := SavedViewDefinition{LibraryID: "reading", Pivot: pivot, Sort: titleAscending(), Presentation: "grid"}
			allow := func(*sql.Tx) error { return nil }
			made, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "reading-view", Kind: "view", Name: &name, Definition: &definition}, allow)
			if err != nil {
				t.Fatal(err)
			}
			rowID := homeViewPrefix + made.ResourceID
			request := HomeRequest{Viewer: viewer, RowOrder: []string{rowID}}
			canonical, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Pivot: pivot, Sort: titleAscending(), Limit: 4})
			if err != nil {
				t.Fatal(err)
			}
			page, err := s.HomeSingleRow(request, rowID, HomeRowPage{Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if rowTotal(page) != 4 || len(page.Entries) != 2 || page.NextCursor == "" {
				t.Fatalf("first page %+v", page)
			}
			next, err := s.HomeSingleRow(request, rowID, HomeRowPage{Limit: 2, Cursor: page.NextCursor})
			if err != nil {
				t.Fatal(err)
			}
			all := append(page.Entries, next.Entries...)
			if len(all) != 4 || next.NextCursor != "" || next.HasMore {
				t.Fatalf("last page %+v", next)
			}
			for i, entry := range all {
				if entry.ID != canonical.Entries[i].ID || entry.Kind != canonical.Entries[i].Kind || entry.Author != canonical.Entries[i].Author || entry.Playback != nil || entry.Duration != nil {
					t.Fatalf("entry %d: %+v canonical %+v", i, entry, canonical.Entries[i])
				}
			}
			denied := request
			denied.Viewer.Libraries = nil
			if _, err = s.HomeSingleRow(denied, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, ErrHomeRowUnknown) {
				t.Fatalf("library revocation %v", err)
			}
			restricted := request
			restricted.Viewer.Restrictions = identity.ContentRestrictions{BlockUnrated: true}
			filtered, err := s.HomeSingleRow(restricted, rowID, HomeRowPage{Limit: 2})
			if err != nil || rowTotal(filtered) != 0 {
				t.Fatalf("restriction revocation %+v %v", filtered, err)
			}
			foreign := request
			foreign.Viewer.Profile = actorKey(ResourceActor{Authority: "local", AccountID: "other", ProfileID: "profile"})
			if _, err = s.HomeSingleRow(foreign, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, ErrHomeRowUnknown) {
				t.Fatalf("foreign owner %v", err)
			}
			collectionName := "Reading collection"
			collection, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "reading-collection", Kind: "collection", Name: &collectionName}, allow)
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{items[3].Public, items[1].Public, items[0].Public, items[2].Public}
			if _, err = s.MutateSavedResource(actor, collection.ResourceID, "entries", SavedResourceMutation{OperationID: "reading-members", ExpectedRevision: collection.Revision, AddItemIDs: ids}, allow); err != nil {
				t.Fatal(err)
			}
			ordinaryName := "Movie collection"
			ordinary, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "movie-collection", Kind: "collection", Name: &ordinaryName}, allow)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.MutateSavedResource(actor, ordinary.ResourceID, "entries", SavedResourceMutation{OperationID: "movie-members", ExpectedRevision: ordinary.Revision, AddItemIDs: []string{movie.Public}}, allow); err != nil {
				t.Fatal(err)
			}
			err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
				picker, e := s.HomeDestinationPickerTx(ctx, dbwork.Snapshot(ctx), "server", viewer, actor, HomeDestinationPickerRequest{Kind: "collection", Limit: 10})
				if e != nil {
					return e
				}
				if len(picker.Rows) != 2 || len(picker.Rows[0].Artwork) != 1 || picker.Rows[0].Artwork[0].ID != movie.Public || len(picker.Rows[1].Artwork) != 4 {
					t.Fatalf("picker %+v", picker)
				}
				for i, entry := range picker.Rows[1].Artwork {
					if entry.ID != ids[i] || entry.Playback != nil || entry.Duration != nil || entry.ReadingMark != nil {
						t.Fatalf("artwork %d %+v", i, entry)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			forbidden := request
			forbidden.ReadingPersonalAuthorize = func(*sql.Tx, string) error { return errors.New("personal authority revoked") }
			if _, err = s.HomeSingleRow(forbidden, rowID, HomeRowPage{Limit: 2}); err == nil {
				t.Fatal("revoked personal authority admitted")
			}
			definition.Sort[0].Direction = "desc"
			if _, err = s.MutateSavedResource(actor, made.ResourceID, "update", SavedResourceMutation{OperationID: "reading-view-update", ExpectedRevision: made.Revision, Definition: &definition}, allow); err != nil {
				t.Fatal(err)
			}
			if _, err = s.HomeSingleRow(request, rowID, HomeRowPage{Limit: 2, Cursor: page.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
				t.Fatalf("changed view continuation %v", err)
			}
			err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
				revoked := viewer
				revoked.Libraries = nil
				picker, e := s.HomeDestinationPickerTx(ctx, dbwork.Snapshot(ctx), "server", revoked, actor, HomeDestinationPickerRequest{Kind: "collection", Limit: 10})
				if e != nil {
					return e
				}
				if len(picker.Rows) != 2 || len(picker.Rows[0].Artwork) != 0 || len(picker.Rows[1].Artwork) != 0 {
					t.Fatalf("revoked artwork %+v", picker)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Keep this destructive refusal probe inside one disposable writer. A
			// rollback restores the original publication and every CASCADE child.
			missing, err := dbwork.Begin(context.Background(), c.DB, dbwork.ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer missing.Rollback()
			deleted, err := missing.Tx().ExecContext(missing.Context(), `DELETE FROM reading_publications WHERE item_id=?`, items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if affected, e := deleted.RowsAffected(); e != nil || affected != 1 {
				t.Fatalf("missing publication probe deleted %d rows: %v", affected, e)
			}
			_, err = s.HomeDestinationPickerTx(missing.Context(), missing.Tx(), "server", viewer, actor, HomeDestinationPickerRequest{Kind: "collection", Limit: 10})
			if !errors.Is(err, ErrReadingProjectionChanged) {
				t.Fatalf("missing publication %v", err)
			}
			if err = missing.Rollback(); err != nil {
				t.Fatal(err)
			}
			err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
				picker, e := s.HomeDestinationPickerTx(ctx, dbwork.Snapshot(ctx), "server", viewer, actor, HomeDestinationPickerRequest{Kind: "collection", Limit: 10})
				if e != nil {
					return e
				}
				if len(picker.Rows) != 2 || len(picker.Rows[1].Artwork) != 4 {
					t.Fatalf("restored publication: %+v", picker)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("restored publication: %v", err)
			}
			c.Exec(`UPDATE reading_attribution_state SET phase='scan',ready_generation=0 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='reading')`)
			err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
				_, e := s.HomeDestinationPickerTx(ctx, dbwork.Snapshot(ctx), "server", viewer, actor, HomeDestinationPickerRequest{Kind: "collection", Limit: 10})
				return e
			})
			if !errors.Is(err, ErrReadingProjectionChanged) {
				t.Fatalf("picker rebuilding %v", err)
			}
			if _, err = s.HomeSingleRow(request, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, ErrReadingProjectionChanged) {
				t.Fatalf("rebuilding %v", err)
			}
		})
	}
}
