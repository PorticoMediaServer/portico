package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

func TestAReusedRowIDNeverShowsTheOldTitle(t *testing.T) {
	s, owner, _ := seededWith(t, 300, 0)
	ctx := context.Background()
	films := libraryOfKind(t, s, owner, "movies")
	ch := makeChannel(t, s, owner, "Films", librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{films}, Kinds: []string{"movie"}}})
	clock := s.now()
	stretch := "/v1/live/guide?from=" + clock.UTC().Format(time.RFC3339) + "&to=" + clock.Add(6*time.Hour).UTC().Format(time.RFC3339)
	var oldTitle, oldID string
	for _, row := range get[LiveGuide](t, s, owner, stretch).Items {
		if row.Channel.ID == ch.ID {
			for _, p := range row.Programmes {
				if p.ItemID != "" {
					oldTitle, oldID = p.Title, p.ItemID
					break
				}
			}
		}
	}
	if oldID == "" {
		t.Fatal("no programme")
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(filepath.Dir(s.setupFile), "main.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `UPDATE items SET public_id = 'itm_reusedrow', title = 'A Different Title' WHERE public_id = ?`, oldID); err != nil {
		t.Fatal(err)
	}
	for _, row := range get[LiveGuide](t, s, owner, stretch).Items {
		if row.Channel.ID != ch.ID {
			continue
		}
		for _, p := range row.Programmes {
			if p.Title == oldTitle || p.ItemID == oldID || p.ItemID == "itm_reusedrow" {
				t.Fatalf("after the row became another title the guide says %+v", p)
			}
		}
	}
	for _, on := range get[Page[LiveOnNow]](t, s, owner, "/v1/live/now").Items {
		if on.Channel.ID == ch.ID && on.Now != nil && (on.Now.Title == oldTitle || on.Now.ItemID == "itm_reusedrow") {
			t.Fatalf("On now says %+v", on.Now)
		}
	}
}
