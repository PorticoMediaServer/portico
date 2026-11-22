package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Select all over a list stands for exactly the titles its pages hold: counted by kind, with
// how many are available, under the list's filters and the viewer's limits. A film whose file
// went is counted and not available; a profile that may see none of it counts nothing.
func TestSelectionCountsAreTheListsOwnTitles(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	signedIn := setup(t, s)
	owner := onMain(t, s, signedIn).AccessToken
	dir := filepath.Join(t.TempDir(), "films")
	os.MkdirAll(dir, 0o755)
	for i, name := range []string{"Heat (1995).mkv", "Alien (1979).mkv", "Brazil (1985).mkv"} {
		os.WriteFile(filepath.Join(dir, name), make([]byte, 100+i), 0o644)
	}
	lib, err := s.db.CreateLibrary(ctx, "films", db.LibraryMovies, "en", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	scanned := func() {
		if _, err := (&scan.Scanner{DB: s.db, Prober: stubProber{}}).Scan(ctx, lib); err != nil {
			t.Fatal(err)
		}
	}
	scanned()
	os.Remove(filepath.Join(dir, "Heat (1995).mkv"))
	scanned()

	list := "kind=movie&library=" + lib.PublicID
	listed := get[ItemPage](t, s, owner, "/v1/items?total=true&"+list)
	counts := get[SelectionCounts](t, s, owner, "/v1/items/selection?sort=added&"+list)
	if listed.Total == nil || counts.Total != *listed.Total || counts.Total != 3 ||
		len(counts.Kinds) != 1 || counts.Kinds[0] != (SelectionKindCount{Kind: "movie", Count: 3, Available: 2}) {
		t.Fatalf("counts %+v, the list holds %v", counts, listed.Total)
	}
	filtered := get[SelectionCounts](t, s, owner, "/v1/items/selection?year=1979,1985&"+list)
	if filtered.Total != 2 || filtered.Kinds[0].Available != 2 {
		t.Fatalf("filtered counts %+v", filtered)
	}

	started, err := s.tokens.CheckAccess(owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	age := 13
	child, err := s.db.CreateSeedProfile(ctx, db.SeedProfile{Name: "child", MaxRatingAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	limited, _ := s.tokens.Access(auth.Claims{UserID: child.UserID, ProfileID: child.ProfileID, Role: "member", Boot: started.Boot},
		time.Now(), time.Now().Add(time.Hour))
	if got := get[SelectionCounts](t, s, limited, "/v1/items/selection?"+list); got.Total != 0 || got.Kinds == nil || len(got.Kinds) != 0 {
		t.Fatalf("a profile that may see none of the films counts %+v", got)
	}
}
