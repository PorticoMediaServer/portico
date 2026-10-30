package metadata

import (
	"context"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A search the owner asked for is the next thing the worker does, ahead of
// titles that are waiting for their first automatic match.
func TestOwnerSearchIsClaimedAheadOfTheBacklog(t *testing.T) {
	s, db, _, _ := integrationScreen(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	library := c.Handle("lib")
	var searched catalogtest.Item
	// The backlog: titles catalogued before and after the one the owner picks.
	for n := 0; n < 6; n++ {
		item := c.Movie(library, "/local/backlog-"+strconv.Itoa(n)+".mkv", "Backlog "+strconv.Itoa(n), 2000+n)
		if n == 4 {
			searched = item
		}
	}
	target := RepairTarget{"item", searched.Public}
	state, err := s.RepairState(ctx, target, integrationOwner)
	if err != nil {
		t.Fatal(err)
	}
	actor := MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	if _, err = s.Repair(ctx, target, RepairCommand{Action: "search", ExpectedRevision: state.Revision, Provider: "tmdb", Query: "Backlog 4"}, actor, integrationOwner); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = db.QueryRow(`SELECT count(*) FROM screen_metadata_work WHERE status='pending' AND target_id<?`, searched.ID).Scan(&pending); err != nil || pending < 4 {
		t.Fatal("fixture has no backlog ahead of the search", pending, err)
	}
	claim, err := s.claimScreen(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if claim.Base.TargetID != searched.Public || claim.Base.Status != "searching" {
		t.Fatalf("the worker took %q (%s) before the owner's search", claim.Base.Title, claim.Base.Status)
	}
}
