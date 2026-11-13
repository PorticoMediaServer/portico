package tvdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tvdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tvdb/tvdbtest"
)

func TestTVDBSearchDetailsEpisodesAndLogin(t *testing.T) {
	stand := tvdbtest.New(t)
	client := tvdb.New("fixture-project-key", stand.URL, 0)
	ctx := context.Background()
	locale := metadata.Locale{Language: "fr", Region: "CA"}
	found, err := client.Search(ctx, metadata.Query{Kind: "show", Title: "Quiet Harbour", Locale: locale})
	if err != nil || len(found) != 1 || found[0].Title != "Le port tranquille" || found[0].Ref.ID != "42" {
		t.Fatalf("search: %+v, %v", found, err)
	}
	found, err = client.Search(ctx, metadata.Query{Kind: "show", IDs: []metadata.Ref{{Provider: "imdb", ID: "tt1234567"}}, Locale: locale})
	if err != nil || len(found) != 1 || !found[0].Exact {
		t.Fatalf("IMDb: %+v, %v", found, err)
	}
	for _, address := range []string{"42", "tvdb:42", "https://thetvdb.com/series/42", "https://www.thetvdb.com/series/quiet-harbour",
		"https://thetvdb.com/?tab=series&id=42&lid=7", "https://thetvdb.com/movies/quiet-harbour/"} {
		ref, err := metadata.ParseRef(address, "tvdb")
		if err != nil || ref.Provider != "tvdb" {
			t.Fatalf("address %s: %+v %v", address, ref, err)
		}
		if ref.Kind == "" {
			ref.Kind = "show"
		}
		record, err := client.Details(ctx, ref, locale)
		if err != nil || record.Ref.ID != "42" || record.Title != "Le port tranquille" || record.Overview != "Une ville côtière." || record.ContentRating != "14+" {
			t.Fatalf("details %s: %+v %v", address, record, err)
		}
		if len(record.Pictures) != 2 || record.Pictures[1].Role != "backdrop" || len(record.Credits) != 1 || len(record.Networks) != 1 || record.Countries[0] != "CA" {
			t.Fatalf("pictures/credits: %+v", record)
		}
	}
	episodes, err := client.Children(ctx, metadata.Ref{Kind: "show", ID: "42"}, 1, locale)
	if err != nil || len(episodes) != 2 || episodes[1].Index != 2 || episodes[0].Ref.ID != "100" || episodes[0].Pictures[0].Role != "thumb" {
		t.Fatalf("episodes: %+v, %v", episodes, err)
	}
	if stand.Logins.Load() != 1 {
		t.Fatalf("login not cached: %d", stand.Logins.Load())
	}
	stand.Expire.Store(true)
	if _, err := client.Search(ctx, metadata.Query{Kind: "show", Title: "Quiet Harbour"}); err != nil || stand.Logins.Load() != 2 {
		t.Fatalf("renewal: %v", err)
	}
	stand.Reject.Store(true)
	before := stand.Requests.Load()
	_, err = client.Search(ctx, metadata.Query{Kind: "show", Title: "Quiet Harbour"})
	var status *fetch.Status
	if !errors.As(err, &status) || status.Code != 401 || stand.Requests.Load()-before != 3 {
		t.Fatalf("renewed more than once: %v", err)
	}
}

func TestTVDBEpisodeReadsAreBounded(t *testing.T) {
	stand := tvdbtest.New(t)
	client := tvdb.New("fixture-project-key", stand.URL, 0)
	ctx := context.Background()
	ref := metadata.Ref{Kind: "show", ID: "42", Order: metadata.OrderDVD}
	if _, err := client.Children(ctx, ref, 1, metadata.Locale{Language: "fr"}); !errors.Is(err, metadata.ErrNoOrder) {
		t.Fatalf("missing order: %v", err)
	}
	stand.Endless.Store(true)
	before := stand.Pages.Load()
	ref.Order = metadata.OrderAired
	records, err := client.Children(ctx, ref, 1, metadata.Locale{Language: "fr"})
	if err == nil || len(records) != 0 || stand.Pages.Load()-before != 20 {
		t.Fatalf("unbounded pages: %d, %v", stand.Pages.Load()-before, err)
	}
}
