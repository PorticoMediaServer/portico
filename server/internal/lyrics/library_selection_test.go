package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLibraryChoicePinsOneRevisionAndKeepsPrivateLyricsOut(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("library", "Music", "music", "/synthetic")
	song := c.Song(c.Album(c.Artist(library, "Artist"), "Album", 2024), 1, "/synthetic/song.flac", "Song")
	c.Drain()
	ctx := context.Background()
	svc := &Service{DB: c.DB}
	owner := Access{Actor: Actor{Authority: "local", AccountID: "owner", ProfileID: "profile", Owner: true}, ServerID: "server", LibraryID: "library", ItemID: song.Public, ViewerFence: "fence", Authorize: func(*sql.Tx) error { return nil }}
	view, err := svc.View(ctx, owner, Target{})
	if err != nil || view.Source == nil {
		t.Fatal(view, err)
	}
	target := Target{SourceID: view.Source.ID, SourceVersion: view.Source.Version}
	first, err := svc.Publish(ctx, owner, Mutation{Target: target, Scope: "library", Language: "en", Format: "text", Text: "First revision"}, "replace")
	if err != nil {
		t.Fatal(err)
	}
	choice := Choose{Target: target, Scope: "library", ResourceID: first.ID, Revision: first.Revision}
	selected, err := svc.Choose(ctx, owner, choice)
	if err != nil || selected.Revision != 1 {
		t.Fatal(selected, err)
	}
	member := owner
	member.Actor = Actor{Authority: "local", AccountID: "member", ProfileID: "member"}
	if _, err = svc.Choose(ctx, member, choice); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("member chose global lyrics", err)
	}
	if _, err = svc.Choose(ctx, owner, choice); !errors.Is(err, ErrConflict) {
		t.Fatal("stale global CAS accepted", err)
	}
	second, err := svc.Publish(ctx, owner, Mutation{Target: target, ResourceID: first.ID, ExpectedRevision: first.Revision, Language: "en", Format: "text", Text: "Second revision"}, "replace")
	if err != nil {
		t.Fatal(err)
	}
	view, err = svc.View(ctx, member, target)
	if err != nil || view.LibrarySelection == nil || view.LibrarySelection.Resource == nil || view.LibrarySelection.Resource.Revision != first.Revision {
		t.Fatal("global revision silently rebound", view, err)
	}
	private, err := svc.Publish(ctx, owner, Mutation{Target: target, Scope: "private", Language: "en", Format: "text", Text: "Private"}, "replace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Choose(ctx, owner, Choose{Target: target, Scope: "library", ExpectedSelectionRevision: 1, ResourceID: private.ID, Revision: private.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatal("private resource published globally", err)
	}
	if _, err = svc.Publish(ctx, owner, Mutation{Target: target, ResourceID: first.ID, ExpectedRevision: second.Revision}, "delete"); err != nil {
		t.Fatal(err)
	}
	view, err = svc.View(ctx, member, target)
	if err != nil || view.LibrarySelection == nil || view.LibrarySelection.Revision != 2 || view.LibrarySelection.Resource != nil {
		t.Fatal("deletion did not clear global choice", view, err)
	}
}
