package catalog

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A Home row's next page survives the decoration that background work writes
// all day (metadata, extras, analysis, artwork), and is still refused once the
// row's membership changes.
func TestHomeRowNextPageSurvivesBackgroundDecoration(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	r.RowOrder = []string{"new_on_server"}
	doc, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	var first HomeRow
	for _, row := range doc.Rows {
		if row.ID == "new_on_server" {
			first = row
		}
	}
	if first.ID == "" {
		t.Fatal("no new_on_server row")
	}
	if first, err = s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1}); err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}

	// Decoration only: a metadata match and a catalogue revision bump of the
	// kind analysis, extras and screen fields make.
	engineMeta(t, f, "m1", "tmdb", "603")
	engineExec(t, f, `UPDATE library_revisions SET revision=revision+5`)
	f.c.Drain()
	second, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Entries) != 1 || second.Entries[0].ID == first.Entries[0].ID {
		t.Fatalf("decoration stranded the next page: %+v %v", second, err)
	}
	if second.Revision != first.Revision {
		t.Fatalf("the paging revision moved with decoration: %+v then %+v", first.Revision, second.Revision)
	}
	if again, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Start: 1, Revision: revisionParam(first.Revision)}); err != nil || len(again.Entries) != 1 {
		t.Fatalf("decoration stranded a ranged page: %+v %v", again, err)
	}

	// A new title changes the row's membership: the old cursor is stale.
	f.c.Movie(f.libs["movies"], "/movies/late.mkv", "Late Arrival", 2026)
	f.c.Drain()
	if _, err = s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("a membership change did not fence the cursor:", err)
	}
}

func revisionParam(r ContentRevision) string {
	return itoa(r.Catalog) + ":" + itoa(r.Viewer)
}

// A computed row's next page survives catalogue writes that move the order
// revision (a scan settling) while its list is unchanged, answers with its first
// page's revision, and is still refused once the list itself changes.
func TestComputedHomeRowNextPageFollowsItsList(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	r.CommunityActivity = true
	engineExec(t, f, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','a',x'00','p',1),('b','b',x'00','q',1),('c','c',x'00','r',1);UPDATE direct_memberships SET allowed_libraries='["movies","tv","music","books"]' WHERE account_id IN('b','c')`)
	shareFixtureProfiles(t, f.db)
	at := r.Now.Add(-time.Hour).Format("2006-01-02T15:04:05.000Z")
	for i, key := range []string{persistence.PersonalOwnerKey("local", "a", "p"), persistence.PersonalOwnerKey("local", "b", "q"), persistence.PersonalOwnerKey("local", "c", "r")} {
		for _, name := range []string{"m1", "e101", "m2"} {
			if name == "m2" && i == 2 {
				continue
			}
			engineExec(t, f, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed)VALUES(?,?,?,?,?,1,600000,0,1)`, fmt.Sprintf("%d-%s", i, name), key, f.items[name].ID, at, at)
		}
	}
	f.c.Drain()
	first, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}

	// The scan settling: order and catalogue revisions move, the list does not.
	engineExec(t, f, `UPDATE library_revisions SET revision=revision+7,order_revision=order_revision+7`)
	second, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Entries) != 1 || second.Entries[0].ID == first.Entries[0].ID {
		t.Fatalf("an unchanged list's next page was refused: %+v %v", second, err)
	}
	if second.Revision != first.Revision {
		t.Fatalf("the continuation answered %+v, not its first page's %+v", second.Revision, first.Revision)
	}

	// The list changes: the old cursor is stale.
	engineExec(t, f, `DELETE FROM personal_history WHERE id='2-m1'`)
	if _, err = s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("a changed list did not fence the cursor:", err)
	}
}
